package masque

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/qpack"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/quicvarint"
)

// HTTP/3 帧类型（RFC 9114）。
const (
	frameData     = 0x00
	frameHeaders  = 0x01
	frameSettings = 0x04
)

// HTTP/3 单向流类型（RFC 9114 / RFC 9204）。
const (
	streamTypeControl      = 0x00
	streamTypeQpackEncoder = 0x02
	streamTypeQpackDecoder = 0x03
)

// HTTP/3 SETTINGS 参数：H3_DATAGRAM（RFC 9297）。
const settingH3Datagram = 0x33

const (
	maxFrameSize   = 1 << 20
	maxDatagramLen = 64 * 1024
	recvQueueDepth = 1024
)

// DefaultPath 是 CONNECT-IP 的默认请求路径（RFC 9484 §3 / 服务端契约）。
const DefaultPath = "/.well-known/masque/ip/0.0.0.0/0/"

// DialConfig 是建立 MASQUE 连接所需的参数。
type DialConfig struct {
	// Endpoint 是节点地址 host:port（建议传已解析的 IP:port，避免隧道接管后 DNS 不可用）。
	Endpoint string
	// SNI 是 TLS ServerName。
	SNI string
	// Token 是节点 JWT（Authorization: Bearer）。
	Token string
	// Path 是 CONNECT-IP 请求路径；为空用 DefaultPath。
	Path string
	// TLS 可指定完整 TLS 配置（证书校验策略由主程序决定）。
	TLS *tls.Config
	// Insecure 在未提供 TLS 时表示跳过证书校验。
	Insecure bool
	// Timeout 是握手 + CONNECT 响应超时（默认 10s）。
	Timeout time.Duration
	// Logger 可选。
	Logger *log.Logger
}

// Conn 是一条 MASQUE CONNECT-IP 隧道；实现主程序的 packetConn 语义
// （SendPacket/RecvPacket/Close），可直接交给 TUN 转发层。
type Conn struct {
	cfg DialConfig
	log *log.Logger

	qconn *quic.Conn
	str   *quic.Stream
	ctx   context.Context
	stop  context.CancelFunc

	writeMu sync.Mutex

	recvCh chan []byte
	done   chan struct{}
	once   sync.Once

	assignedMu sync.Mutex
	assigned   []AssignedAddress
	assignedCh chan struct{}

	routesMu sync.Mutex
	routes   []Route

	droppedDown atomic.Uint64
	droppedUp   atomic.Uint64
	gotDatagram atomic.Bool

	// 服务端响应头（tunnel/node 会带 X-VPN-Assigned-IPv4 / X-VPN-MTU）。
	respMTU      int
	respAssigned string
}

// Dial 建立 QUIC/HTTP3 连接并完成 CONNECT-IP 协商。
func Dial(ctx context.Context, cfg DialConfig) (*Conn, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}
	dctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	tlsConf := cfg.TLS
	if tlsConf == nil {
		tlsConf = &tls.Config{
			ServerName:         cfg.SNI,
			NextProtos:         []string{"h3"},
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: cfg.Insecure,
		}
	} else {
		tlsConf = tlsConf.Clone()
		if len(tlsConf.NextProtos) == 0 {
			tlsConf.NextProtos = []string{"h3"}
		}
	}
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
	qconn, err := quic.DialAddr(dctx, cfg.Endpoint, tlsConf, quicConf)
	if err != nil {
		return nil, fmt.Errorf("masque: QUIC 连接节点失败: %w", err)
	}

	c := &Conn{
		cfg:        cfg,
		log:        logger,
		qconn:      qconn,
		recvCh:     make(chan []byte, recvQueueDepth),
		done:       make(chan struct{}),
		assignedCh: make(chan struct{}),
	}
	c.ctx, c.stop = context.WithCancel(context.Background())

	if err := c.setupControlStream(dctx); err != nil {
		_ = qconn.CloseWithError(0, "setup failed")
		return nil, err
	}
	if err := c.openConnectStream(dctx); err != nil {
		_ = qconn.CloseWithError(0, "connect failed")
		return nil, err
	}
	go c.readLoop()
	go c.datagramLoop()
	return c, nil
}

// setupControlStream 打开控制流并发送 SETTINGS（H3_DATAGRAM=1），
// 同时打开 QPACK 编/解码流（静态表模式，不发动态表指令）。
func (c *Conn) setupControlStream(ctx context.Context) error {
	uni, err := c.qconn.OpenUniStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("masque: 打开控制流失败: %w", err)
	}
	// 控制流必须保持打开；先写流类型，再写 SETTINGS 帧。
	if _, err := uni.Write(quicvarint.Append(nil, streamTypeControl)); err != nil {
		_ = uni.Close()
		return fmt.Errorf("masque: 写控制流类型失败: %w", err)
	}
	settings := quicvarint.Append(nil, settingH3Datagram)
	settings = quicvarint.Append(settings, 1)
	if err := writeFrame(uni, frameSettings, settings); err != nil {
		_ = uni.Close()
		return fmt.Errorf("masque: 写 SETTINGS 失败: %w", err)
	}
	// QPACK 编/解码流：仅打开，静态表模式不需要指令。
	for _, typ := range []uint64{streamTypeQpackEncoder, streamTypeQpackDecoder} {
		s, err := c.qconn.OpenUniStreamSync(ctx)
		if err != nil {
			return fmt.Errorf("masque: 打开 QPACK 流失败: %w", err)
		}
		if _, err := s.Write(quicvarint.Append(nil, typ)); err != nil {
			_ = s.Close()
			return fmt.Errorf("masque: 写 QPACK 流类型失败: %w", err)
		}
	}
	return nil
}

// openConnectStream 在双向流上发送扩展 CONNECT 请求，并读取响应头。
func (c *Conn) openConnectStream(ctx context.Context) error {
	str, err := c.qconn.OpenStreamSync(ctx)
	if err != nil {
		return fmt.Errorf("masque: 打开请求流失败: %w", err)
	}
	c.str = str

	host := c.cfg.Endpoint
	req, err := newConnectRequest(host, c.cfg.Path, c.cfg.Token)
	if err != nil {
		return err
	}
	headerBlock, err := encodeRequestHeader(req)
	if err != nil {
		return err
	}
	if err := writeFrame(str, frameHeaders, headerBlock); err != nil {
		return fmt.Errorf("masque: 写 CONNECT 请求头失败: %w", err)
	}

	// 读响应头（可能先收到若干非 HEADERS 帧，按 RFC 跳过未知帧）。
	for {
		typ, payload, err := readFrame(str)
		if err != nil {
			return fmt.Errorf("masque: 读取 CONNECT 响应失败: %w", err)
		}
		if typ != frameHeaders {
			continue
		}
		status, headers, err := decodeResponseHeader(payload)
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return fmt.Errorf("masque: 服务端拒绝 CONNECT-IP（HTTP %d）", status)
		}
		if proto := headers.Get("capsule-protocol"); proto != "" && proto != "?1" {
			return fmt.Errorf("masque: 服务端 capsule-protocol=%q（期望 ?1）", proto)
		}
		c.respAssigned = strings.TrimSpace(headers.Get("x-vpn-assigned-ipv4"))
		if v := strings.TrimSpace(headers.Get("x-vpn-mtu")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				c.respMTU = n
			}
		}
		c.log.Printf("masque: CONNECT-IP 已建立（HTTP %d，path=%s）", status, c.cfg.Path)
		return nil
	}
}

// SendPacket 把内层 IP 包通过 HTTP Datagram 发出（Context ID=0）。
// 发送失败（缓冲满/连接关闭）只丢弃并计数，不阻断 TUN 读循环。
func (c *Conn) SendPacket(pkt []byte) error {
	if len(pkt) == 0 || len(pkt) > maxDatagramLen {
		return fmt.Errorf("masque: 报文长度非法（%d）", len(pkt))
	}
	select {
	case <-c.done:
		return io.ErrClosedPipe
	default:
	}
	// 直接走 QUIC DATAGRAM：需要自己补 Quarter Stream ID（RFC 9297），
	// 然后才是 Context ID（=0）+ IP 包。
	payload := quicvarint.Append(nil, uint64(c.str.StreamID()/4))
	payload = AppendDatagramPayload(payload, pkt)
	if err := c.qconn.SendDatagram(payload); err != nil {
		if n := c.droppedUp.Add(1); n%1000 == 1 {
			c.log.Printf("masque: 上行 DATAGRAM 发送失败（累计 %d）：%v", n, err)
		}
		return nil
	}
	return nil
}

// RecvPacket 阻塞读取一个内层 IP 包（来自 HTTP Datagram）。
func (c *Conn) RecvPacket() ([]byte, error) {
	select {
	case pkt := <-c.recvCh:
		return pkt, nil
	case <-c.done:
		return nil, io.EOF
	}
}

// Close 关闭 MASQUE 连接并等待后台协程退出。
func (c *Conn) Close() error {
	c.once.Do(func() {
		c.stop()
		_ = c.qconn.CloseWithError(0, "client closed")
		close(c.done)
	})
	return nil
}

// QUICConn 返回底层 QUIC 连接（用于客户端统一做 RTT/统计/保活监控）。
func (c *Conn) QUICConn() *quic.Conn { return c.qconn }

// Assigned 返回服务端 ADDRESS_ASSIGN 下发的地址（尚未收到时为空）。
func (c *Conn) Assigned() []AssignedAddress {
	c.assignedMu.Lock()
	defer c.assignedMu.Unlock()
	out := make([]AssignedAddress, len(c.assigned))
	copy(out, c.assigned)
	return out
}

// WaitAssigned 等待服务端下发 ADDRESS_ASSIGN（或 ctx 超时）。
func (c *Conn) WaitAssigned(ctx context.Context) error {
	select {
	case <-c.assignedCh:
		return nil
	case <-c.done:
		return io.EOF
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Routes 返回最近一次 ROUTE_ADVERTISEMENT（仅供诊断/日志）。
func (c *Conn) Routes() []Route {
	c.routesMu.Lock()
	defer c.routesMu.Unlock()
	out := make([]Route, len(c.routes))
	copy(out, c.routes)
	return out
}

// Stats 返回丢包统计（上行发送失败、下行队列溢出）。
func (c *Conn) Stats() (droppedUp, droppedDown uint64) {
	return c.droppedUp.Load(), c.droppedDown.Load()
}

// MTU 返回服务端响应头 X-VPN-MTU 宣告的隧道 MTU（未宣告返回 0）。
func (c *Conn) MTU() int { return c.respMTU }

// AssignedIPv4Header 返回服务端响应头 X-VPN-Assigned-IPv4（未返回为空）。
func (c *Conn) AssignedIPv4Header() string { return c.respAssigned }

// readLoop 读取请求流上的 HTTP/3 帧；CONNECT-IP 的 capsules 都在 DATA 帧里。
func (c *Conn) readLoop() {
	var capBuf []byte
	for {
		typ, payload, err := readFrame(c.str)
		if err != nil {
			select {
			case <-c.done:
				return
			default:
			}
			c.log.Printf("masque: 读取数据面失败：%v", err)
			_ = c.Close()
			return
		}
		switch typ {
		case frameData:
			capBuf = append(capBuf, payload...)
			var err error
			capBuf, err = c.consumeCapsules(capBuf)
			if err != nil {
				c.log.Printf("masque: capsule 解析失败：%v", err)
				_ = c.Close()
				return
			}
		case frameHeaders:
			// 响应 trailers，忽略。
		default:
			// 其它帧（SETTINGS 不会出现在请求流）忽略。
		}
	}
}

// consumeCapsules 从缓冲区里尽可能多地解析 capsule，返回剩余未完成字节。
func (c *Conn) consumeCapsules(buf []byte) ([]byte, error) {
	for {
		capsule, consumed, ok, err := parseOneCapsule(buf)
		if err != nil {
			return nil, err
		}
		if !ok {
			return buf, nil
		}
		buf = buf[consumed:]
		switch capsule.Type {
		case CapsuleAddressAssign:
			addrs, err := DecodeAddressAssign(capsule.Value)
			if err != nil {
				return nil, err
			}
			c.assignedMu.Lock()
			c.assigned = addrs
			c.assignedMu.Unlock()
			select {
			case <-c.assignedCh:
			default:
				close(c.assignedCh)
			}
			for _, a := range addrs {
				c.log.Printf("masque: ADDRESS_ASSIGN %s/%d (request=%d)", a.IP, a.PrefixLen, a.RequestID)
			}
		case CapsuleRouteAdvertisement:
			routes, err := DecodeRouteAdvertisement(capsule.Value)
			if err != nil {
				return nil, err
			}
			c.routesMu.Lock()
			c.routes = routes
			c.routesMu.Unlock()
			for _, r := range routes {
				c.log.Printf("masque: ROUTE_ADVERTISEMENT %s - %s proto=%d", r.IP, r.EndIP, r.Protocol)
			}
		case CapsuleDatagram:
			ctx, data, err := ParseDatagramPayload(capsule.Value)
			if err != nil || ctx != 0 {
				continue
			}
			c.pushDownstream(data)
		default:
			// 未知 capsule 忽略（RFC 9297 允许）。
		}
	}
}

// datagramLoop 接收服务端 HTTP Datagram（Context ID=0 → IP 包）。
func (c *Conn) datagramLoop() {
	for {
		select {
		case <-c.done:
			return
		default:
		}
		b, err := c.qconn.ReceiveDatagram(c.ctx)
		if err != nil {
			select {
			case <-c.done:
				return
			default:
			}
			_ = c.Close()
			return
		}
		quarter, n, err := quicvarint.Parse(b)
		if err != nil {
			continue
		}
		if quarter != uint64(c.str.StreamID()/4) {
			continue
		}
		_, data, err := ParseDatagramPayload(b[n:])
		if err != nil {
			continue
		}
		c.gotDatagram.Store(true)
		c.pushDownstream(data)
	}
}

func (c *Conn) pushDownstream(pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	cp := make([]byte, len(pkt))
	copy(cp, pkt)
	select {
	case c.recvCh <- cp:
	default:
		if n := c.droppedDown.Add(1); n%1000 == 1 {
			c.log.Printf("masque: 下行队列已满，丢弃报文（累计 %d）", n)
		}
	}
}

// writeCapsule 把一条 capsule 写进请求流（HTTP DATA 帧）。
func (c *Conn) writeCapsule(typ uint64, value []byte) error {
	body := AppendCapsule(nil, typ, value)
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writeFrame(c.str, frameData, body)
}

// SendAddressRequest 请求服务端分配指定地址（可选；服务端也可能主动下发）。
func (c *Conn) SendAddressRequest(reqs []RequestedAddress) error {
	payload, err := EncodeAddressRequest(reqs)
	if err != nil {
		return err
	}
	return c.writeCapsule(CapsuleAddressRequest, payload)
}

// ---- HTTP/3 帧与 QPACK 辅助 ----

func writeFrame(w io.Writer, typ uint64, payload []byte) error {
	hdr := quicvarint.Append(nil, typ)
	hdr = quicvarint.Append(hdr, uint64(len(payload)))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (uint64, []byte, error) {
	typ, err := readVarint(r)
	if err != nil {
		return 0, nil, err
	}
	length, err := readVarint(r)
	if err != nil {
		return 0, nil, err
	}
	if length > maxFrameSize {
		return 0, nil, fmt.Errorf("masque: HTTP/3 帧过大（%d）", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return typ, payload, nil
}

// parseOneCapsule 从缓冲区解析一条 capsule；数据不完整时 ok=false。
func parseOneCapsule(buf []byte) (Capsule, int, bool, error) {
	if len(buf) == 0 {
		return Capsule{}, 0, false, nil
	}
	typ, n, err := quicvarint.Parse(buf)
	if err != nil {
		return Capsule{}, 0, false, nil // 类型 varint 不完整
	}
	off := n
	if off >= len(buf) {
		return Capsule{}, 0, false, nil
	}
	length, n2, err := quicvarint.Parse(buf[off:])
	if err != nil {
		return Capsule{}, 0, false, nil
	}
	off += n2
	if length > maxCapsuleSize {
		return Capsule{}, 0, false, fmt.Errorf("masque: capsule 过大（%d）", length)
	}
	total := off + int(length)
	if total > len(buf) {
		return Capsule{}, 0, false, nil
	}
	value := make([]byte, length)
	copy(value, buf[off:total])
	return Capsule{Type: typ, Value: value}, total, true, nil
}

// newConnectRequest 构造扩展 CONNECT 请求（:protocol=connect-ip）。
func newConnectRequest(host, path, token string) (map[string]string, error) {
	if host == "" || path == "" {
		return nil, errors.New("masque: host/path 为空")
	}
	req := map[string]string{
		":method":          "CONNECT",
		":protocol":        "connect-ip",
		":scheme":          "https",
		":authority":       host,
		":path":            path,
		"user-agent":       "ProxyPilot-MASQUE/1.0",
		"capsule-protocol": "?1",
		"h3-datagram":      "?1",
	}
	if token != "" {
		req["authorization"] = "Bearer " + token
	}
	return req, nil
}

// encodeRequestHeader 用 QPACK 编码请求头（只使用静态表/字面量，无动态表）。
func encodeRequestHeader(req map[string]string) ([]byte, error) {
	order := []string{":method", ":protocol", ":scheme", ":authority", ":path", "authorization", "capsule-protocol", "h3-datagram", "user-agent"}
	var buf bytes.Buffer
	enc := qpack.NewEncoder(&buf)
	for _, name := range order {
		value, ok := req[name]
		if !ok {
			continue
		}
		if err := enc.WriteField(qpack.HeaderField{Name: name, Value: value}); err != nil {
			return nil, fmt.Errorf("masque: QPACK 编码失败: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("masque: QPACK 收尾失败: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeResponseHeader 解码响应 HEADERS；返回状态码与全部头字段。
func decodeResponseHeader(block []byte) (int, headerMap, error) {
	var fields []qpack.HeaderField
	dec := qpack.NewDecoder(func(f qpack.HeaderField) {})
	var err error
	fields, err = dec.DecodeFull(block)
	_ = dec.Close()
	if err != nil {
		return 0, nil, fmt.Errorf("masque: QPACK 解码响应头失败: %w", err)
	}
	hm := headerMap{}
	for _, f := range fields {
		hm[f.Name] = f.Value
	}
	status := 0
	if v, ok := hm[":status"]; ok {
		fmt.Sscanf(v, "%d", &status)
	}
	if status == 0 {
		return 0, nil, errors.New("masque: 响应缺少 :status")
	}
	return status, hm, nil
}

// headerMap 是大小写不敏感的简化头表（HTTP/3 头名为小写）。
type headerMap map[string]string

func (h headerMap) Get(name string) string { return h[name] }
