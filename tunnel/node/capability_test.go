package node

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/quic-go/quic-go"
)

// closeCountConn 是可记录 CloseWithError 次数的 datagramConn 测试替身。
type closeCountConn struct {
	mu         sync.Mutex
	closeCalls int
	closedMsg  string
}

func (c *closeCountConn) SendDatagram([]byte) error { return nil }

func (c *closeCountConn) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *closeCountConn) CloseWithError(_ quic.ApplicationErrorCode, msg string) error {
	c.mu.Lock()
	c.closeCalls++
	c.closedMsg = msg
	c.mu.Unlock()
	return nil
}

func (c *closeCountConn) Context() context.Context { return context.Background() }

func (c *closeCountConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

// P1-6 回归：能力协商必须「先读 H3-Datagram 再决定是否注册 datagram 会话」。
func TestNegotiateDatagramRequiresBothSides(t *testing.T) {
	cases := []struct {
		name           string
		nodeEnabled    bool
		clientDeclared bool
		want           bool
	}{
		{name: "双方都支持", nodeEnabled: true, clientDeclared: true, want: true},
		{name: "客户端未声明（老客户端）", nodeEnabled: true, clientDeclared: false, want: false},
		{name: "节点关闭 DATAGRAM", nodeEnabled: false, clientDeclared: true, want: false},
		{name: "双方都不支持", nodeEnabled: false, clientDeclared: false, want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newSessionSetTestServer()
			srv.cfg.DatagramEnabled = c.nodeEnabled
			if got := srv.negotiateDatagram(c.clientDeclared); got != c.want {
				t.Fatalf("negotiateDatagram(%v)=%v，期望 %v", c.clientDeclared, got, c.want)
			}
		})
	}
}

// P1-6 回归（源码顺序守卫）：修复前先在 stream.go 里注册 datagram 会话、
// 再读能力头判断，随后 dropDatagramChannel 会关掉承载隧道流的共享 QUIC 连接。
// 这里用源码顺序断言防止回退。
func TestStreamNegotiatesBeforeRegisteringDatagramSession(t *testing.T) {
	raw, err := os.ReadFile("stream.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	readIdx := strings.Index(src, `r.Header.Get("H3-Datagram")`)
	regIdx := strings.Index(src, "s.register(dsess)")
	if readIdx < 0 {
		t.Fatal("stream.go 里找不到 H3-Datagram 能力头读取")
	}
	if regIdx < 0 {
		t.Fatal("stream.go 里找不到 datagram 会话注册")
	}
	if readIdx > regIdx {
		t.Fatal("必须先读 H3-Datagram 能力头，再注册 datagram 会话；" +
			"反过来会注册后立刻回退并关掉正在用的共享 QUIC 连接")
	}
	if !strings.Contains(src, "s.dropDatagramChannel(ip, curConn)") {
		t.Fatal("回退时必须把当前连接传给 dropDatagramChannel，否则无法区分「共享连接」与「旧连接」")
	}

	rawSrv, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rawSrv), "cur.conn != nil && cur.conn != keepConn") {
		t.Fatal("dropDatagramChannel 必须跳过与当前隧道流共用的连接（不能关它）")
	}
}

// P1-6 回归：回退时只清 datagram 槽位，不能关掉正在承载隧道流的共享连接；
// 清槽位后 pick() 要回退到可靠流。
func TestDropDatagramChannelKeepsSharedConnection(t *testing.T) {
	cases := []struct {
		name           string
		withStream     bool
		withDatagram   bool
		sameConn       bool
		wantDGClosed   bool
		wantSetDeleted bool
	}{
		{
			name: "残留 datagram 在另一条旧连接上", withStream: true, withDatagram: true,
			wantDGClosed: true,
		},
		{
			name: "datagram 与隧道流共用当前连接", withStream: true, withDatagram: true, sameConn: true,
			wantDGClosed: false,
		},
		{
			name: "只有 datagram 且是旧连接", withDatagram: true,
			wantDGClosed: true, wantSetDeleted: true,
		},
		{
			name: "本来就没有 datagram 会话", withStream: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := newSessionSetTestServer()
			ip := net.ParseIP("10.88.3.10").To4()
			dgConn := &closeCountConn{}
			if c.withStream {
				srv.register(newTestSession(ip, kindStream))
			}
			if c.withDatagram {
				srv.register(&session{kind: kindDatagram, ip: ip, conn: dgConn, send: dgConn.SendDatagram})
			}
			var keepConn datagramConn
			if c.sameConn {
				keepConn = dgConn
			}
			srv.dropDatagramChannel(ip, keepConn)

			if c.withDatagram {
				if got := dgConn.closeCount() > 0; got != c.wantDGClosed {
					t.Fatalf("连接 CloseWithError 调用=%v，期望 %v（共享连接绝不能被关）", got, c.wantDGClosed)
				}
			}
			set := srv.lookup(ip)
			if c.wantSetDeleted {
				if set != nil {
					t.Fatal("只有 datagram 会话时，清槽位后应删除整条记录")
				}
				return
			}
			if set == nil {
				t.Fatal("可靠流还在，集合不应被删除")
			}
			if set.datagram != nil {
				t.Fatal("datagram 槽位应被清空")
			}
			if c.withStream {
				if got := set.pick(mkPkt(17)); got != set.stream {
					t.Fatal("清掉 datagram 后 UDP 下行必须回退到可靠流")
				}
			}
		})
	}
}
