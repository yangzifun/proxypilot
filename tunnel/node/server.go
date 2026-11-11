package node

import (
	"sync/atomic"
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

// sessionKind 区分同一设备的两条承载通道。
//
// 36.0 起数据面双通道：TCP 走可靠流（POST /tunnel），UDP 走 QUIC DATAGRAM（GET /）。
// 两者属于同一设备、同一个 IP，必须并存而不是互相接管。
type sessionKind int

const (
	kindStream   sessionKind = iota + 1 // POST /tunnel 可靠流
	kindDatagram                        // GET / DATAGRAM
)

func (k sessionKind) String() string {
	switch k {
	case kindStream:
		return "stream"
	case kindDatagram:
		return "datagram"
	default:
		return "unknown"
	}
}

type session struct {
	jti     string
	kind    sessionKind
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
	// sendFail 统计下行发送失败次数（DATAGRAM 缓冲满/连接关闭等），
	// 是「节点发了但客户端没收到」的唯一线索。
	sendFail atomic.Uint64
}

// Server 是自研 HTTP/3 隧道节点（非标准 MASQUE）。
type Server struct {
	cfg      Config
	log      *slog.Logger
	verifier *Verifier
	tun      tunDevice
	dns      *dnsRelay

	mu       sync.Mutex
	sessions map[string]*sessionSet // ip4 -> 双通道会话集合

	srv *http3.Server
	ln  *quic.EarlyListener

	// udpConn / transport：自建的 UDP socket 与 quic 传输层
	// （为了放大收发缓冲，并启用批量收发）
	udpConn   *net.UDPConn
	transport *quic.Transport

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
		sessions: make(map[string]*sessionSet),
		done:     make(chan struct{}),
	}, nil
}

// Run 启动 TUN、QUIC/HTTP3 监听与数据面，阻塞直到 ctx 结束。
// socketBufferBytes 是 UDP 收发缓冲大小。
//
// BDP = 带宽 × RTT。100 Mbps × 150 ms ≈ 1.9 MB，跨境链路留余量给 4 MB。
// 默认 64 KB 会把吞吐锁在约 3~4 Mbps。
const socketBufferBytes = 4 << 20

// applySocketBuffer 尽力放大 UDP 收发缓冲（部分系统会截断到上限，不影响正确性）。
func applySocketBuffer(c *net.UDPConn) {
	if c == nil {
		return
	}
	_ = c.SetReadBuffer(socketBufferBytes)
	_ = c.SetWriteBuffer(socketBufferBytes)
}

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

	// ---- 轻量 DNS 转发：监听网关 53，供客户端把域名解析交给节点 ----
	if s.cfg.DNSRelay == nil || *s.cfg.DNSRelay {
		relay := newDNSRelay(net.JoinHostPort(s.cfg.Gateway4, "53"), s.cfg.DNSUpstream, s.log)
		if err := relay.Start(); err != nil {
			// 不致命：DNS 转发起不来不该让整条隧道失效，客户端还能用公共 DNS
			s.log.Warn("DNS 转发启动失败，客户端将回退到公共 DNS", "err", err)
		} else {
			s.dns = relay
			defer relay.Close()
		}
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
	// 自建 UDP socket 并放大缓冲：BDP = 带宽 × RTT，
	// 100Mbps × 150ms ≈ 1.9MB，默认 64KB 会把吞吐锁死在几 Mbps。
	// 用 quic.Transport 接管后还能启用 Linux 上的 GSO 批量发送与 recvmmsg。
	udpAddr, aerr := net.ResolveUDPAddr("udp4", s.cfg.Listen)
	if aerr != nil {
		_ = dev.Close()
		return fmt.Errorf("解析监听地址 %s: %w", s.cfg.Listen, aerr)
	}
	udpConn, cerr := net.ListenUDP("udp4", udpAddr)
	if cerr != nil {
		_ = dev.Close()
		return fmt.Errorf("监听 %s: %w", s.cfg.Listen, cerr)
	}
	applySocketBuffer(udpConn)
	s.udpConn = udpConn
	s.transport = &quic.Transport{Conn: udpConn}

	ln, err := s.transport.ListenEarly(tlsConf, quicConf)
	if err != nil {
		_ = udpConn.Close()
		_ = dev.Close()
		return fmt.Errorf("监听 %s: %w", s.cfg.Listen, err)
	}
	s.ln = ln
	s.srv = &http3.Server{
		TLSConfig:  tlsConf,
		QUICConfig: quicConf,
		// HTTP/3 层必须显式开启 datagram，否则不协商 SETTINGS_H3_DATAGRAM，
		// 客户端 ReceiveDatagram 一律返回 "datagram support disabled"。
		// 光设 quic.Config.EnableDatagrams 不够（quic-go http3/server.go:137）。
		EnableDatagrams: true,
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
		if s.transport != nil {
			_ = s.transport.Close()
		}
		if s.udpConn != nil {
			_ = s.udpConn.Close()
		}
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
		jti: claims.JTI, kind: kindDatagram, ip: ip, device: claims.Subject,
		grantID: claims.GrantID, conn: conn, started: time.Now(),
	}
	sess.send = func(p []byte) error { return sess.conn.SendDatagram(p) }
	sess.closeFn = func() { _ = sess.conn.CloseWithError(0, "session closed") }

	// 面板关掉 DATAGRAM 时**不要注册 datagram 会话**。
	// 否则 tunLoop 的 pick() 会继续把 UDP 下行塞进 DATAGRAM，
	// 而客户端此时只读可靠流 —— UDP 回包（DNS 应答）全被静默丢弃，
	// 表现就是「ICMP 通、网页打不开」。
	// 不注册后 pick() 自动回退到可靠流，两端行为一致。
	if !s.cfg.DatagramEnabled {
		h := w.Header()
		h.Set("X-VPN-Auth", "Success")
		h.Set("X-VPN-Assigned-IPv4", ip.String())
		h.Set("X-VPN-Max-Datagram", "0")
		h.Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		return
	}

	if !s.register(sess) {
		http.Error(w, "ip already in use", http.StatusConflict)
		return
	}

	h := w.Header()
	h.Set("X-VPN-Auth", "Success")
	h.Set("X-VPN-Assigned-IPv4", ip.String())
	h.Set("X-VPN-Max-Datagram", fmt.Sprintf("%d", s.cfg.MaxDatagram))
	h.Set("Capsule-Protocol", "?1")
	h.Set("H3-Datagram", "?1")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	go s.readSession(sess)
	s.log.Info("会话建立", "ip", ip.String(), "device", claims.Subject,
		"node", claims.NodeID, "remote", r.RemoteAddr)
}

// sessionSet 是同一设备的两条承载通道。
//
// 选路原则：UDP 优先走 DATAGRAM（不被可靠流的队头阻塞拖累），
// 其余（TCP/ICMP）走可靠流；某一路未建立时**回退**到另一路，
// 避免静默丢包。
type sessionSet struct {
	stream   *session
	datagram *session
}

// pick 按内层协议号选通道。
//
// proto 取自内层 IPv4 头的协议字段（6=TCP, 17=UDP, 1=ICMP）。
func (set *sessionSet) pick(proto byte) *session {
	if set == nil {
		return nil
	}
	if proto == 17 && set.datagram != nil && set.datagram.send != nil {
		return set.datagram
	}
	if set.stream != nil && set.stream.send != nil {
		return set.stream
	}
	if set.datagram != nil && set.datagram.send != nil {
		return set.datagram
	}
	return nil
}

// slot 返回该 kind 对应的槽位指针（用于写入）。
func (set *sessionSet) slot(kind sessionKind) **session {
	if kind == kindDatagram {
		return &set.datagram
	}
	return &set.stream
}

// other 返回另一条通道（用于发送失败时回退）。
func (set *sessionSet) other(s *session) *session {
	if set == nil {
		return nil
	}
	if s != nil && s.kind == kindDatagram {
		return set.stream
	}
	if s != nil && s.kind == kindStream {
		return set.datagram
	}
	if set.stream != nil {
		return set.stream
	}
	return set.datagram
}

// empty 表示两条通道都没了。
func (set *sessionSet) empty() bool {
	return set == nil || (set.stream == nil && set.datagram == nil)
}

func (s *Server) register(sess *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sess.ip.String()
	set := s.sessions[key]
	if set == nil {
		set = &sessionSet{}
		s.sessions[key] = set
	}
	// 只替换**同一种承载**的旧会话。
	// 客户端重启后旧会话往往来不及清理（对端进程被杀），同 kind 直接接管，
	// 否则会一直 409；不同 kind 必须并存（双通道）。
	slot := set.slot(sess.kind)
	if cur := *slot; cur != nil {
		s.log.Warn("接管同通道旧会话", "ip", key, "kind", sess.kind.String(),
			"old_device", cur.device, "old_duration", time.Since(cur.started).Round(time.Second))
		if cur.conn != nil {
			_ = cur.conn.CloseWithError(0, "session replaced")
		}
	}
	*slot = sess
	s.log.Info("会话注册", "ip", key, "kind", sess.kind.String())
	return true
}

func (s *Server) unregister(sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := sess.ip.String()
	set := s.sessions[key]
	if set == nil {
		return
	}
	// 只清自己那个槽位；另一条通道还在就保留集合
	slot := set.slot(sess.kind)
	if *slot == sess {
		*slot = nil
	}
	if set.empty() {
		delete(s.sessions, key)
	}
}

func (s *Server) lookup(ip net.IP) *sessionSet {
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
	var writeFail int
	lastStats := time.Now()
	for {
		data, err := sess.conn.ReceiveDatagram(sess.conn.Context())
		if err != nil {
			s.log.Info("会话数据面结束", "ip", sess.ip.String(), "err", err.Error(),
				"rx", sess.rx, "tx", sess.tx,
				"dropped", sess.sendFail.Load()+uint64(writeFail),
				"bad_pkt", badPkt, "bad_src", badSrc)
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
			// 命名对齐可靠流那边的「流隧道流量」，方便两侧对照排查：
			//   tx      上行：客户端发来、已写入 TUN 的包数
			//   rx      下行：节点发回客户端的包数（由 tunLoop 计数）
			//   dropped 上行写 TUN 失败 + 下行发送失败
			s.log.Info("DATAGRAM 流量", "ip", sess.ip.String(),
				"rx", sess.rx, "tx", sess.tx,
				"dropped", sess.sendFail.Load()+uint64(writeFail),
				"bad_pkt", badPkt, "bad_src", badSrc)
			lastStats = time.Now()
		}
		if _, werr := s.tun.Write(pkt); werr != nil {
			// 单个报文写失败（例如超过 TUN MTU）不能终止会话，
			// 否则后续所有流量都会被静默丢弃、表现为「已连接但完全不通」。
			writeFail++
			if writeFail == 1 {
				s.log.Warn("写入 TUN 失败", "err", werr, "len", len(pkt), "ip", sess.ip.String())
			}
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
		set := s.lookup(dst)
		if set == nil {
			continue
		}
		// 按内层协议号选通道：UDP 走 DATAGRAM，其余走可靠流；某一路缺失则回退
		var proto byte
		if len(pkt) >= 10 {
			proto = pkt[9]
		}
		sess := set.pick(proto)
		sent := false
		if sess != nil && sess.send != nil {
			if err := sess.send(pkt); err == nil {
				sess.rx++
				sent = true
			} else {
				sess.sendFail.Add(1)
			}
		}
		if !sent {
			// 首选通道发不出去（连接已关闭 / DATAGRAM 缓冲满）时**回退到另一条通道**。
			// 没有这层回退时，一条通道坏掉就等于下行全黑：
			// 实测出现过 frames=553 tx=552 但 rx=50 —— 节点把上行都转发进 TUN 了，
			// 下行却因为 pick() 选中的通道发不出去而几乎全部丢弃，
			// 客户端表现为「网页打不开、ping 也不通」。
			alt := set.other(sess)
			if alt != nil && alt.send != nil {
				if err := alt.send(pkt); err == nil {
					alt.rx++
					sent = true
				} else {
					alt.sendFail.Add(1)
				}
			}
		}
	}
}

func inCIDR(ip net.IP, cidr string) bool {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return false
	}
	return n.Contains(ip)
}
