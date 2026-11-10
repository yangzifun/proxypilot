package node

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// tunDevice 是 TUN 设备的最小接口（便于测试替换）。
type tunDevice interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
	Name() string
}

// datagramConn 是数据面真正用到的 QUIC 能力（窄接口，便于测试替换）。
type datagramConn interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(quic.ApplicationErrorCode, string) error
	Context() context.Context
}

type connKey struct{}

type session struct {
	jti     string
	ip      net.IP
	device  string
	grantID int64
	conn    datagramConn
	// send / closeFn 由具体传输（DATAGRAM 或可靠流）注入
	send    func([]byte) error
	closeFn func()
	started time.Time
	tx      uint64
	rx      uint64
}

// Server 是自研 HTTP/3 隧道节点（非标准 MASQUE）。
type Server struct {
	cfg      Config
	log      *slog.Logger
	verifier *Verifier
	tun      tunDevice

	mu       sync.Mutex
	sessions map[string]*session // ip4 -> session

	srv *http3.Server
	ln  *quic.EarlyListener

	closeOnce sync.Once
	done      chan struct{}
}

// New 构造节点服务（不启动）。
func New(cfg Config, log *slog.Logger) (*Server, error) {
	v, err := NewVerifier(cfg.PublicKey, cfg.Issuer, cfg.Audience)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:      cfg,
		log:      log,
		verifier: v,
		sessions: make(map[string]*session),
		done:     make(chan struct{}),
	}, nil
}

// Run 启动 TUN、QUIC/HTTP3 监听与数据面，阻塞直到 ctx 结束。
func (s *Server) Run(ctx context.Context) error {
	cert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		return fmt.Errorf("加载 TLS 证书: %w", err)
	}
	dev, err := openTUN(s.cfg.TunName, s.cfg.MTU)
	if err != nil {
		return fmt.Errorf("打开 TUN %s: %w", s.cfg.TunName, err)
	}
	s.tun = dev

	if s.cfg.Configure {
		if err := configureTUN(s.cfg); err != nil {
			_ = dev.Close()
			return fmt.Errorf("配置 TUN/NAT: %w", err)
		}
		defer cleanupTUN(s.cfg)
	}

	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h3"},
	}
	// 与客户端保持一致的流控窗口（接收方窗口才决定吞吐上限）
	quicConf := &quic.Config{
		EnableDatagrams:                true,
		HandshakeIdleTimeout:           10 * time.Second,
		MaxIdleTimeout:                 120 * time.Second,
		KeepAlivePeriod:                15 * time.Second,
		InitialStreamReceiveWindow:     4 << 20,
		MaxStreamReceiveWindow:         16 << 20,
		InitialConnectionReceiveWindow: 8 << 20,
		MaxConnectionReceiveWindow:     64 << 20,
	}
	ln, err := quic.ListenAddrEarly(s.cfg.Listen, tlsConf, quicConf)
	if err != nil {
		_ = dev.Close()
		return fmt.Errorf("监听 %s: %w", s.cfg.Listen, err)
	}
	s.ln = ln
	s.srv = &http3.Server{
		TLSConfig:  tlsConf,
		QUICConfig: quicConf,
		Handler:    s,
		ConnContext: func(ctx context.Context, c *quic.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}

	go s.tunLoop()
	s.log.Info("HTTP/3 隧道节点已启动", "listen", s.cfg.Listen, "tun", dev.Name(),
		"client_cidr", s.cfg.ClientCIDR, "gateway", s.cfg.Gateway4)

	errCh := make(chan error, 1)
	go func() { errCh <- s.srv.ServeListener(ln) }()

	select {
	case <-ctx.Done():
		s.Close()
		return nil
	case err := <-errCh:
		s.Close()
		if err != nil && !strings.Contains(err.Error(), "closed") {
			return err
		}
		return nil
	}
}

// Close 关闭监听与 TUN。
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.srv != nil {
			_ = s.srv.Close()
		}
		if s.ln != nil {
			_ = s.ln.Close()
		}
		if s.tun != nil {
			_ = s.tun.Close()
		}
	})
}

// ServeHTTP 处理客户端的 HTTP/3 鉴权请求。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/tunnel" {
		s.serveStream(w, r)
		return
	}
	if r.Method != http.MethodGet || r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(raw, "Bearer ") {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	claims, err := s.verifier.Verify(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")))
	if err != nil {
		s.log.Warn("连接令牌校验失败", "err", err, "remote", r.RemoteAddr)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.cfg.NodeID > 0 && claims.NodeID != s.cfg.NodeID {
		http.Error(w, "wrong node", http.StatusForbidden)
		return
	}
	ip := net.ParseIP(claims.IPv4).To4()
	if ip == nil || !inCIDR(ip, s.cfg.ClientCIDR) {
		http.Error(w, "ip not in client cidr", http.StatusForbidden)
		return
	}
	conn, _ := r.Context().Value(connKey{}).(*quic.Conn)
	if conn == nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	sess := &session{
		jti: claims.JTI, ip: ip, device: claims.Subject, grantID: claims.GrantID,
		conn: conn, started: time.Now(),
	}
	sess.send = func(p []byte) error { return sess.conn.SendDatagram(p) }
	sess.closeFn = func() { _ = sess.conn.CloseWithError(0, "session closed") }
	if !s.register(sess) {
		http.Error(w, "ip already in use", http.StatusConflict)
		return
	}

	h := w.Header()
	h.Set("X-VPN-Auth", "Success")
	h.Set("X-VPN-Assigned-IPv4", ip.String())
	h.Set("X-VPN-Max-Datagram", fmt.Sprintf("%d", s.cfg.MaxDatagram))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	go s.readSession(sess)
	s.log.Info("会话建立", "ip", ip.String(), "device", claims.Subject,
		"node", claims.NodeID, "remote", r.RemoteAddr)
}

func (s *Server) register(sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.sessions[sess.ip.String()]; ok {
		// 同一地址只允许一个活会话；客户端重启后旧会话往往来不及清理（对端进程被杀），
		// 这里直接接管，否则会一直 409，节点永远连不回来。
		s.log.Warn("接管组网会话", "ip", sess.ip.String(), "old_device", cur.device,
			"old_duration", time.Since(cur.started).Round(time.Second))
		if cur.conn != nil {
			_ = cur.conn.CloseWithError(0, "session replaced")
		}
	}
	s.sessions[sess.ip.String()] = sess
	return true
}

func (s *Server) unregister(sess *session) {
	s.mu.Lock()
	if cur, ok := s.sessions[sess.ip.String()]; ok && cur == sess {
		delete(s.sessions, sess.ip.String())
	}
	s.mu.Unlock()
}

func (s *Server) lookup(ip net.IP) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[ip.String()]
}

// readSession 消费该客户端的 DATAGRAM，校验源地址后写入 TUN。
func (s *Server) readSession(sess *session) {
	defer func() {
		s.unregister(sess)
		if sess.closeFn != nil {
			sess.closeFn()
		} else if sess.conn != nil {
			_ = sess.conn.CloseWithError(0, "session closed")
		}
		s.log.Info("会话结束", "ip", sess.ip, "device", sess.device,
			"duration", time.Since(sess.started).Round(time.Second))
	}()
	var badPkt, badSrc int
	lastStats := time.Now()
	for {
		data, err := sess.conn.ReceiveDatagram(sess.conn.Context())
		if err != nil {
			s.log.Info("会话数据面结束", "ip", sess.ip.String(), "err", err.Error(),
				"rx", sess.rx, "tx", sess.tx, "bad_pkt", badPkt, "bad_src", badSrc)
			return
		}
		pkt, src, _, perr := StripPadding(data)
		if perr != nil {
			badPkt++
			if badPkt == 1 {
				s.log.Warn("丢弃无法解析的报文", "ip", sess.ip.String(), "len", len(data), "err", perr.Error())
			}
			continue
		}
		if !src.Equal(sess.ip) {
			badSrc++
			if badSrc == 1 {
				s.log.Warn("丢弃源地址不符的报文", "sess_ip", sess.ip.String(), "src", src.String())
			}
			continue
		}
		if time.Since(lastStats) > 30*time.Second {
			s.log.Info("会话流量", "ip", sess.ip.String(), "rx", sess.rx, "tx", sess.tx,
				"bad_pkt", badPkt, "bad_src", badSrc)
			lastStats = time.Now()
		}
		if _, werr := s.tun.Write(pkt); werr != nil {
			// 单个报文写失败（例如超过 TUN MTU）不能终止会话，
			// 否则后续所有流量都会被静默丢弃、表现为「已连接但完全不通」。
			s.log.Warn("写入 TUN 失败", "err", werr, "len", len(pkt), "ip", sess.ip.String())
			continue
		}
		sess.tx++
	}
}

// tunLoop 读取 TUN，把下行报文发给对应客户端；客户端互访直接丢弃。
func (s *Server) tunLoop() {
	buf := make([]byte, 65535)
	for {
		n, err := s.tun.Read(buf)
		if err != nil {
			select {
			case <-s.done:
			default:
				s.log.Warn("读取 TUN 失败", "err", err)
			}
			return
		}
		pkt := buf[:n]
		_, src, dst, err := StripPadding(pkt)
		if err != nil {
			continue
		}
		// 只在「客户端 → 客户端」时按隔离策略丢弃；访问网关或公网必须放行。
		// 旧逻辑只看源地址，导致所有出站流量（源地址就是客户端 IP）被整包丢弃。
		srcClient := inCIDR(src, s.cfg.ClientCIDR)
		dstClient := inCIDR(dst, s.cfg.ClientCIDR)
		// 网关（节点自身）的应答也要放行：ping 网关、网关发起的回包
		gatewayIP := net.ParseIP(strings.TrimSpace(s.cfg.Gateway4))
		touchesGateway := gatewayIP != nil && (src.Equal(gatewayIP) || dst.Equal(gatewayIP))
		if srcClient && dstClient && !touchesGateway && !s.cfg.AllowClientToClient {
			continue
		}
		sess := s.lookup(dst)
		if sess == nil {
			continue
		}
		if sess.send == nil {
			continue
		}
		if err := sess.send(pkt); err != nil {
			continue
		}
		sess.rx++
	}
}

func inCIDR(ip net.IP, cidr string) bool {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return n.Contains(ip)
}
