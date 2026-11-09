package node

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

// 回归测试：tunLoop 的客户端隔离不能把「客户端访问网关/公网」的包丢掉。
// 历史 bug：只判断源地址在 ClientCIDR 内就 continue，导致所有出站包被丢弃。

type fakeTUN struct {
	mu      sync.Mutex
	packets [][]byte
	ch      chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newFakeTUN() *fakeTUN {
	return &fakeTUN{ch: make(chan []byte, 16), closed: make(chan struct{})}
}

func (f *fakeTUN) Read(p []byte) (int, error) {
	select {
	case pkt, ok := <-f.ch:
		if !ok {
			return 0, io.EOF
		}
		f.mu.Lock()
		f.packets = append(f.packets, pkt)
		f.mu.Unlock()
		return copy(p, pkt), nil
	case <-f.closed:
		return 0, io.EOF
	}
}

func (f *fakeTUN) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeTUN) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}
func (f *fakeTUN) Name() string { return "faketun0" }

// fakeConn 记录节点发回给客户端的 DATAGRAM。
type fakeConn struct {
	mu  sync.Mutex
	out [][]byte
}

func (c *fakeConn) SendDatagram(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]byte, len(b))
	copy(cp, b)
	c.out = append(c.out, cp)
	return nil
}

func (c *fakeConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *fakeConn) CloseWithError(code quic.ApplicationErrorCode, msg string) error { return nil }
func (c *fakeConn) Context() context.Context                                       { return context.Background() }

func (c *fakeConn) sent() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte{}, c.out...)
}

// buildIPv4 构造最小 IPv4 报文（仅用于地址解析）。
func buildIPv4(src, dst string, payload byte) []byte {
	pkt := make([]byte, 20, 21)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:], 21)
	pkt[8] = 64
	pkt[9] = 1 // ICMP
	copy(pkt[12:16], net.ParseIP(src).To4())
	copy(pkt[16:20], net.ParseIP(dst).To4())
	return append(pkt, payload)
}

func newTestServer(t *testing.T) (*Server, *fakeTUN) {
	t.Helper()
	srv := &Server{
		cfg: Config{
			ClientCIDR:          "10.88.1.0/24",
			Gateway4:            "10.88.1.1",
			AllowClientToClient: false,
		},
		log:      slog.Default(),
		sessions: make(map[string]*session),
	}
	tun := newFakeTUN()
	srv.tun = tun
	srv.done = make(chan struct{})
	go srv.tunLoop()
	t.Cleanup(func() {
		close(srv.done)
		_ = tun.Close()
	})
	return srv, tun
}

func registerSession(srv *Server, ip string, conn *fakeConn) {
	srv.mu.Lock()
	sess := &session{ip: net.ParseIP(ip).To4(), started: time.Now()}
	sess.send = conn.SendDatagram
	srv.sessions[ip] = sess
	srv.mu.Unlock()
}

func waitSent(conn *fakeConn, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(conn.sent()) > 0 {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func TestTunLoopForwardsInternetReply(t *testing.T) {
	srv, tun := newTestServer(t)
	conn := &fakeConn{}
	registerSession(srv, "10.88.1.12", conn)

	// 公网 → 客户端（例如 ping 1.1.1.1 的回包）：源地址不在客户端网段，必须放行
	tun.ch <- buildIPv4("1.1.1.1", "10.88.1.12", 0x00)

	if !waitSent(conn, 2*time.Second) {
		t.Fatal("公网回包被丢弃")
	}
}

func TestTunLoopForwardsGatewayReply(t *testing.T) {
	srv, tun := newTestServer(t)
	conn := &fakeConn{}
	registerSession(srv, "10.88.1.12", conn)

	// 网关 → 客户端（ping 网关的回包）：必须放行
	tun.ch <- buildIPv4("10.88.1.1", "10.88.1.12", 0x00)

	if !waitSent(conn, 2*time.Second) {
		t.Fatal("网关回包被丢弃")
	}
}

func TestTunLoopBlocksClientToClient(t *testing.T) {
	srv, tun := newTestServer(t)
	conn := &fakeConn{}
	registerSession(srv, "10.88.1.12", conn)
	registerSession(srv, "10.88.1.13", &fakeConn{})

	tun.ch <- buildIPv4("10.88.1.12", "10.88.1.13", 0x11)

	time.Sleep(300 * time.Millisecond)
	if len(conn.sent()) != 0 {
		t.Fatal("客户端互访应被隔离，但报文被转发了")
	}
}