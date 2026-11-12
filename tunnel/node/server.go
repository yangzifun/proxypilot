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
	// lane 是客户端多路复用时的流编号：0 = 主可靠流，>0 = 附加独立流。
	lane    int
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

	// fecEnc 是**该会话独享**的下行 FEC 编码器（仅 kindDatagram 使用）。
	//
	// 曾经在 tunLoop 里只建一个全局共享编码器：一个 FEC 组里混入发往不同客户端的
	// 包，凑满组时校验帧只发给「触发凑满的那个包」的会话，其余客户端永远缺包，
	// Decode 判定 missingCount>1 直接放弃 —— FEC 只烧 1/K 带宽、完全无法还原。
	// 解码端（readSession / 客户端 dualPacketConn）本来就是每会话一个解码器，
	// 编码器必须按同样粒度分组。只在 tunLoop 单协程里读写，无需加锁。
	fecEnc *fecEncoder
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
	// lanes 是多路复用的附加可靠流（lane>=1），与主可靠流互相独立：
	// 每条都是独立 QUIC 流，各有自己的流控与重传队列，一条丢包不连累其他。
	lanes map[int]*session
}

// pick 按内层协议号选通道。
//
// proto 取自内层 IPv4 头的协议字段（6=TCP, 17=UDP, 1=ICMP）。
func (set *sessionSet) pick(pkt []byte) *session {
	if set == nil {
		return nil
	}
	proto := innerProto(pkt)
	if proto == 17 && set.datagram != nil && set.datagram.send != nil {
		return set.datagram
	}
	// 多路复用：按五元组哈希选 lane，与客户端的 flowLane 完全一致，
	// 保证「下行回包写回客户端发它时用的那条流」。
	if len(set.lanes) > 0 {
		if s := set.lanes[flowLane(pkt, multiLaneCount)]; s != nil && s.send != nil {
			return s
		}
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

// writeDatagramToTun 把一条已解码的内层报文写入 TUN，并做源地址校验。
//
// 从 readSession 抽出来，是因为引入 FEC 后一个 DATAGRAM 可能产出 0 或 1 个
// 完整报文，校验与写入逻辑需要被复用，避免复制粘贴两份。
func (s *Server) writeDatagramToTun(sess *session, data []byte, badPkt, badSrc, writeFail *int) {
	pkt, src, _, perr := StripPadding(data)
	if perr != nil {
		*badPkt++
		if *badPkt == 1 {
			s.log.Warn("丢弃无法解析的报文", "ip", sess.ip.String(), "len", len(data), "err", perr.Error())
		}
		return
	}
	if !src.Equal(sess.ip) {
		*badSrc++
		if *badSrc == 1 {
			s.log.Warn("丢弃源地址不符的报文", "sess_ip", sess.ip.String(), "src", src.String())
		}
		return
	}
	if _, werr := s.tun.Write(pkt); werr != nil {
		*writeFail++
		if *writeFail == 1 {
			s.log.Warn("写入 TUN 失败", "err", werr, "len", len(pkt), "ip", sess.ip.String())
		}
		return
	}
	sess.tx++
}
// multiLaneCount 必须与客户端 vpnclient.multiLaneCount 保持一致。
const multiLaneCount = 3

// innerProto 取内层 IPv4 头的协议号；取不到返回 0。
func innerProto(pkt []byte) byte {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0
	}
	return pkt[9]
}

// flowLane 与客户端同算法：同一条内层连接永远落在同一条 lane 上。
func flowLane(pkt []byte, n int) int {
	if n <= 1 || len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0
	}
	ihl := int(pkt[0]&0x0f) * 4
	h := uint32(2166136261)
	for _, b := range pkt[12:16] {
		h = (h ^ uint32(b)) * 16777619
	}
	if len(pkt) >= ihl+4 {
		for _, b := range pkt[ihl : ihl+4] {
			h = (h ^ uint32(b)) * 16777619
		}
	}
	return int(h % uint32(n))
}

// isDNSPacket 判断是否为 DNS 报文（UDP 且源/目的端口为 53）。
//
// 与客户端 vpnclient/dual_tunnel.go 的同名函数保持一致的判定，
// 保证两个方向的 DNS 都走可靠流。
func isDNSPacket(pkt []byte) bool {
	if len(pkt) < 28 || pkt[0]>>4 != 4 || pkt[9] != 17 {
		return false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return false
	}
	sport := int(pkt[ihl])<<8 | int(pkt[ihl+1])
	dport := int(pkt[ihl+2])<<8 | int(pkt[ihl+3])
	return sport == 53 || dport == 53
}
// negotiateDatagram 判断本次隧道流是否启用 DATAGRAM：
// 节点开了 DATAGRAM 且客户端在请求头声明支持，两者缺一不可。
//
// 必须在注册 datagram 会话之前调用：先注册后判断会走「注册了再回退」的路径，
// 而回退要清掉共享连接上的会话，容易误关正在承载隧道流的连接。
func (s *Server) negotiateDatagram(clientDeclared bool) bool {
	return s.cfg.DatagramEnabled && clientDeclared
}

// dropDatagramChannel 清掉某设备的 datagram 槽位，让 pick() 回退到可靠流。
//
// 用于「客户端明确不支持 DATAGRAM」时：必须让 pick() 回退到可靠流，
// 否则节点会继续把 UDP 下行塞进 DATAGRAM，而客户端不读 datagram，
// 回包直接消失 —— 实测表现为 nslookup 全部超时、ping 公网 IP 正常。
//
// keepConn 是本次请求正在使用的连接（DATAGRAM 与隧道流共用同一条 QUIC 连接）：
// **绝不能关闭它**，否则会把刚建立的隧道流一起断掉。只有残留的 datagram 会话
// 跑在别的连接上时才关闭旧连接（那才是安全的清理）。
func (s *Server) dropDatagramChannel(ip net.IP, keepConn datagramConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ip.String()
	set := s.sessions[key]
	if set == nil || set.datagram == nil {
		return
	}
	cur := set.datagram
	set.datagram = nil
	if set.empty() {
		delete(s.sessions, key)
	}
	if cur.conn != nil && cur.conn != keepConn {
		_ = cur.conn.CloseWithError(0, "client datagram unsupported")
	}
	s.log.Info("已停用 DATAGRAM 通道", "ip", key, "reason", "客户端未声明支持")
}
// empty 表示两条通道都没了。
func (set *sessionSet) empty() bool {
	return set == nil || (set.stream == nil && set.datagram == nil && len(set.lanes) == 0)
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
	if sess.kind == kindStream && sess.lane > 0 {
		if set.lanes == nil {
			set.lanes = make(map[int]*session)
		}
		if cur := set.lanes[sess.lane]; cur != nil && cur.conn != nil {
			_ = cur.conn.CloseWithError(0, "lane replaced")
		}
		set.lanes[sess.lane] = sess
		s.log.Info("lane 会话注册", "ip", key, "lane", sess.lane)
		return true
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
	if sess.kind == kindStream && sess.lane > 0 {
		if set.lanes != nil && set.lanes[sess.lane] == sess {
			delete(set.lanes, sess.lane)
		}
		if set.empty() {
			delete(s.sessions, key)
		}
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
	// 每个 datagram 会话一个解码器，按 session 生命周期存活。
	// FEC 是「81% 丢包」的补救手段：DATAGRAM 不重传，只有冗余能救。
	var fecDec *fecDecoder
	if s.cfg.FECMode == "xor" && s.cfg.FECGroup > 1 {
		fecDec = newFECDecoder()
	}
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
		frames := [][]byte{data}
		if fecDec != nil {
			decoded, ferr := fecDec.Decode(data)
			if ferr != nil {
				badPkt++
				if badPkt == 1 {
					s.log.Warn("FEC 解码失败", "ip", sess.ip.String(), "len", len(data), "err", ferr.Error())
				}
				continue
			}
			if len(decoded) == 0 {
				continue
			}
			frames = decoded
		}
		for _, frame := range frames {
			s.writeDatagramToTun(sess, frame, &badPkt, &badSrc, &writeFail)
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
	}
}

// tunLoop 读取 TUN，把下行报文发给对应客户端；客户端互访直接丢弃。
func (s *Server) tunLoop() {
	buf := make([]byte, 65535)
	// 下行 FEC 开关（仅 DATAGRAM 通道使用）；编码器按会话持有，见 session.fecEnc。
	fecDownOn := s.cfg.FECMode == "xor" && s.cfg.FECGroup > 1
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
		// DNS 固定走可靠流：DATAGRAM 不重传，实测 81% 的 UDP 回包丢失，
		// 而 DNS 客户端 2 秒超时，丢了就是用户等好几秒。其余 UDP 仍走 DATAGRAM。
		sess := set.pick(pkt)
		if isDNSPacket(pkt) {
			if set.stream != nil && set.stream.send != nil {
				sess = set.stream
			}
		}
		sent := false
		// 下行 FEC：只作用于 DATAGRAM 通道，每组校验帧都发给**同一个会话**。
		// 组内丢 1 个可由对端还原 —— 这是 81% 丢包唯一的补救手段。
		// 校验帧比原始报文多 4+K*2 字节头，超 MaxDatagram 时跳过（否则必然发送失败，
		// 该组失去恢复能力且日志无痕）。
		if fecDownOn && sess != nil && sess.kind == kindDatagram && sess.send != nil && len(pkt) <= s.cfg.MaxDatagram {
			if sess.fecEnc == nil {
				sess.fecEnc = newFECEncoder(s.cfg.FECGroup)
			}
			data, parity := sess.fecEnc.Encode(pkt)
			if err := sess.send(data); err == nil {
				sess.rx++
				if len(parity) > 0 && len(parity) <= s.cfg.MaxDatagram {
					_ = sess.send(parity)
				}
				continue
			}
			sess.sendFail.Add(1)
		}
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
