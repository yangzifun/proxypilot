package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"
)

// 轻量 DNS 转发器：在内网网关上（例如 10.88.2.1:53）监听，
// 把客户的查询原样转发给上游解析器，再把应答原样送回。
//
// 为什么要它：客户端配置里 dns 声明的就是这个网关地址，
// 但节点上原本没有任何进程监听它 —— 客户端连上后所有解析都超时
// （实测香港2 的 53 只有 warp-svc 与 systemd-resolved 在本地回环上）。
//
// 刻意不做 DNS 报文解析：只搬字节，因此支持任意记录类型、
// 也不会因为解析器 bug 破坏 EDNS/压缩指针。开销主要在每查询一个临时 socket，
// 对个人 VPN 的查询量完全够用，并用信号量限制并发防止被刷爆。

const (
	dnsRelayMaxInflight = 512
	dnsRelayQueryWait   = 4 * time.Second
	dnsRelayUpstreamTTL = 0 // 上游列表由配置决定，不做缓存
)

type dnsRelay struct {
	listenAddr string
	upstreams  []string
	log        *slog.Logger

	udp *net.UDPConn
	tcp net.Listener

	sem      chan struct{}
	wg       sync.WaitGroup
	closeOne sync.Once
	done     chan struct{}

	// 统计（只用于日志）
	queries uint64
	failed  uint64
}

// newDNSRelay 创建转发器；listen 形如 10.88.2.1:53。
func newDNSRelay(listenAddr string, upstreams []string, log *slog.Logger) *dnsRelay {
	ups := normalizeUpstreams(upstreams)
	if len(ups) == 0 {
		ups = []string{"1.1.1.1:53", "223.5.5.5:53"}
	}
	return &dnsRelay{
		listenAddr: listenAddr,
		upstreams:  ups,
		log:        log,
		sem:        make(chan struct{}, dnsRelayMaxInflight),
		done:       make(chan struct{}),
	}
}

// normalizeUpstreams 补全端口并去重。
func normalizeUpstreams(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(u); err != nil {
			u = net.JoinHostPort(u, "53")
		}
		if seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// Start 启动 UDP 与 TCP 监听。任一失败都返回错误（由调用方决定是否致命）。
func (d *dnsRelay) Start() error {
	ua, err := net.ResolveUDPAddr("udp4", d.listenAddr)
	if err != nil {
		return fmt.Errorf("DNS 转发：地址非法 %s: %w", d.listenAddr, err)
	}
	uc, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return fmt.Errorf("DNS 转发：监听 UDP %s 失败: %w", d.listenAddr, err)
	}
	d.udp = uc

	// TCP 也开：部分系统在应答被截断时会自动改用 TCP
	if ln, terr := net.Listen("tcp4", d.listenAddr); terr == nil {
		d.tcp = ln
	} else {
		d.log.Warn("DNS 转发：TCP 监听失败，仅提供 UDP", "addr", d.listenAddr, "err", terr)
	}

	d.wg.Add(1)
	go d.serveUDP()
	if d.tcp != nil {
		d.wg.Add(1)
		go d.serveTCP()
	}
	d.log.Info("DNS 转发已启动", "listen", d.listenAddr, "upstream", strings.Join(d.upstreams, ","))
	return nil
}

func (d *dnsRelay) Close() {
	d.closeOne.Do(func() {
		close(d.done)
		if d.udp != nil {
			_ = d.udp.Close()
		}
		if d.tcp != nil {
			_ = d.tcp.Close()
		}
	})
	d.wg.Wait()
}

func (d *dnsRelay) serveUDP() {
	defer d.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, src, err := d.udp.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-d.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		if n < 12 { // 比 DNS 头还短，丢掉
			continue
		}
		q := make([]byte, n)
		copy(q, buf[:n])

		select {
		case d.sem <- struct{}{}:
		default:
			// 并发已满：直接丢弃，让客户端自己重试（DNS 本身会重传）
			continue
		}
		d.wg.Add(1)
		go func(query []byte, client *net.UDPAddr) {
			defer d.wg.Done()
			defer func() { <-d.sem }()
			d.handleUDP(query, client)
		}(q, src)
	}
}

// handleUDP 一次查询：向上游逐个尝试，拿到应答就回给客户端。
func (d *dnsRelay) handleUDP(query []byte, client *net.UDPAddr) {
	resp, err := d.exchangeUDP(query)
	if err != nil {
		d.failed++
		if d.failed%200 == 1 {
			d.log.Warn("DNS 转发：上游无应答", "upstream", strings.Join(d.upstreams, ","),
				"client", client.String(), "err", err)
		}
		return
	}
	d.queries++
	if _, werr := d.udp.WriteToUDP(resp, client); werr != nil {
		d.log.Warn("DNS 转发：回写客户端失败", "client", client.String(), "err", werr)
	}
}

// exchangeUDP 依次询问上游，返回第一个有效应答。
func (d *dnsRelay) exchangeUDP(query []byte) ([]byte, error) {
	var lastErr error
	for _, up := range d.upstreams {
		ua, err := net.ResolveUDPAddr("udp4", up)
		if err != nil {
			lastErr = err
			continue
		}
		// 每查询一个临时 socket：源端口随机，天然避免应答串包
		conn, err := net.DialUDP("udp4", nil, ua)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(dnsRelayQueryWait))
		if _, err := conn.Write(query); err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		_ = conn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if n < 12 {
			lastErr = fmt.Errorf("上游应答过短：%d", n)
			continue
		}
		out := make([]byte, n)
		copy(out, buf[:n])
		return out, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用上游")
	}
	return nil, lastErr
}

func (d *dnsRelay) serveTCP() {
	defer d.wg.Done()
	for {
		c, err := d.tcp.Accept()
		if err != nil {
			select {
			case <-d.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		d.wg.Add(1)
		go func(conn net.Conn) {
			defer d.wg.Done()
			defer conn.Close()
			d.handleTCP(conn)
		}(c)
	}
}

// handleTCP 处理 TCP 上的 DNS（2 字节长度前缀），支持单连接多次查询。
func (d *dnsRelay) handleTCP(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var hdr [2]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if n < 12 || n > 4096 {
			return
		}
		q := make([]byte, n)
		if _, err := io.ReadFull(conn, q); err != nil {
			return
		}
		resp, err := d.exchangeTCP(q)
		if err != nil {
			d.failed++
			return
		}
		out := make([]byte, 2+len(resp))
		binary.BigEndian.PutUint16(out[:2], uint16(len(resp)))
		copy(out[2:], resp)
		if _, err := conn.Write(out); err != nil {
			return
		}
	}
}

func (d *dnsRelay) exchangeTCP(query []byte) ([]byte, error) {
	var lastErr error
	for _, up := range d.upstreams {
		conn, err := net.DialTimeout("tcp4", up, 3*time.Second)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(dnsRelayQueryWait))
		out := make([]byte, 2+len(query))
		binary.BigEndian.PutUint16(out[:2], uint16(len(query)))
		copy(out[2:], query)
		if _, err := conn.Write(out); err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}
		var hdr [2]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}
		n := int(binary.BigEndian.Uint16(hdr[:]))
		if n < 12 || n > 4096 {
			_ = conn.Close()
			lastErr = fmt.Errorf("应答长度非法：%d", n)
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(conn, buf); err != nil {
			_ = conn.Close()
			lastErr = err
			continue
		}
		_ = conn.Close()
		return buf, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用上游")
	}
	return nil, lastErr
}

// waitReady 等到 UDP 监听就绪或超时（仅用于测试/启动顺序）。
func (d *dnsRelay) waitReady(ctx context.Context) error {
	for {
		if d.udp != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}