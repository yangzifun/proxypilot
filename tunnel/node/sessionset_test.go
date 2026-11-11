package node

import (
	"io"
	"log/slog"
	"net"
	"testing"
)

// newTestServer 造一个带静默 logger 的 Server（register 里会打日志）。
func newSessionSetTestServer() *Server {
	return &Server{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[string]*sessionSet),
	}
}

// mkPkt 造一个最小 IPv4 报文（20 字节头 + 8 字节头），
// 只为了让 pick() 能读到协议号与端口做选路。
func mkPkt(proto byte) []byte {
	p := make([]byte, 28)
	p[0] = 0x45
	p[9] = proto
	return p
}

func newTestSession(ip net.IP, kind sessionKind) *session {
	return &session{
		kind: kind,
		ip:   ip,
		send: func([]byte) error { return nil },
	}
}

// 双通道并存：同一 IP 的 stream 与 datagram 必须同时存在，不能互相接管。
func TestSessionSetCoexist(t *testing.T) {
	srv := newSessionSetTestServer()
	ip := net.ParseIP("10.88.3.10").To4()

	dsess := newTestSession(ip, kindDatagram)
	if !srv.register(dsess) {
		t.Fatal("datagram 注册失败")
	}
	ssess := newTestSession(ip, kindStream)
	if !srv.register(ssess) {
		t.Fatal("stream 注册失败")
	}

	set := srv.lookup(ip)
	if set == nil {
		t.Fatal("集合应存在")
	}
	if set.datagram != dsess {
		t.Fatal("datagram 槽位被顶掉了")
	}
	if set.stream != ssess {
		t.Fatal("stream 槽位被顶掉了")
	}
}

// 选路：UDP(17) 走 datagram，TCP(6) 与 ICMP(1) 走 stream。
func TestSessionSetPick(t *testing.T) {
	ip := net.ParseIP("10.88.3.10").To4()
	dsess := newTestSession(ip, kindDatagram)
	ssess := newTestSession(ip, kindStream)
	set := &sessionSet{stream: ssess, datagram: dsess}

	if got := set.pick(mkPkt(17)); got != dsess {
		t.Fatal("UDP 应走 datagram")
	}
	if got := set.pick(mkPkt(6)); got != ssess {
		t.Fatal("TCP 应走 stream")
	}
	if got := set.pick(mkPkt(1)); got != ssess {
		t.Fatal("ICMP 应走 stream")
	}
}

// 回退：datagram 缺失时 UDP 也要能走 stream，不能静默丢包。
func TestSessionSetFallback(t *testing.T) {
	ip := net.ParseIP("10.88.3.10").To4()
	ssess := newTestSession(ip, kindStream)

	onlyStream := &sessionSet{stream: ssess}
	if got := onlyStream.pick(mkPkt(17)); got != ssess {
		t.Fatal("datagram 缺失时 UDP 应回退到 stream")
	}

	dsess := newTestSession(ip, kindDatagram)
	onlyDatagram := &sessionSet{datagram: dsess}
	if got := onlyDatagram.pick(mkPkt(6)); got != dsess {
		t.Fatal("stream 缺失时应回退到 datagram")
	}

	if got := (*sessionSet)(nil).pick(mkPkt(6)); got != nil {
		t.Fatal("空集合应返回 nil")
	}
}

// 注销只清自己的槽位；两条都没了才删除整条记录。
func TestSessionSetUnregister(t *testing.T) {
	srv := newSessionSetTestServer()
	ip := net.ParseIP("10.88.3.10").To4()

	dsess := newTestSession(ip, kindDatagram)
	ssess := newTestSession(ip, kindStream)
	srv.register(dsess)
	srv.register(ssess)

	// 注销 stream：datagram 必须还在
	srv.unregister(ssess)
	set := srv.lookup(ip)
	if set == nil {
		t.Fatal("datagram 还在，集合不应被删除")
	}
	if set.stream != nil {
		t.Fatal("stream 槽位应已清空")
	}
	if set.datagram != dsess {
		t.Fatal("datagram 不应被误删")
	}

	// 再注销 datagram：整条记录清掉
	srv.unregister(dsess)
	if srv.lookup(ip) != nil {
		t.Fatal("两条通道都没了，记录应删除")
	}
}

// 同 kind 重连：新会话接管旧的，另一条通道不受影响。
func TestSessionSetSameKindTakeover(t *testing.T) {
	srv := newSessionSetTestServer()
	ip := net.ParseIP("10.88.3.10").To4()

	oldStream := newTestSession(ip, kindStream)
	dsess := newTestSession(ip, kindDatagram)
	srv.register(oldStream)
	srv.register(dsess)

	newStream := newTestSession(ip, kindStream)
	srv.register(newStream)

	set := srv.lookup(ip)
	if set.stream != newStream {
		t.Fatal("同 kind 新会话应接管")
	}
	if set.datagram != dsess {
		t.Fatal("另一条通道不应被影响")
	}
}