package node

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// newDownlinkFECTestServer 造一个已启动 tunLoop 的节点（FEC 参数必须在启动前设好：
// tunLoop 只在入口读一次 FEC 开关）。
func newDownlinkFECTestServer(t *testing.T, group int) (*Server, *fakeTUN) {
	t.Helper()
	srv := &Server{
		cfg: Config{
			ClientCIDR:          "10.88.1.0/24",
			Gateway4:            "10.88.1.1",
			DatagramEnabled:     true,
			FECMode:             "xor",
			FECGroup:            group,
			MaxDatagram:         1500,
			AllowClientToClient: false,
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: make(map[string]*sessionSet),
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

// registerDownlinkDatagram 注册一个 datagram 会话（只关心下行 sess.send）。
func registerDownlinkDatagram(srv *Server, ip string, conn *fakeConn) {
	sess := &session{kind: kindDatagram, ip: net.ParseIP(ip).To4(), conn: conn, started: time.Now()}
	sess.send = conn.SendDatagram
	srv.register(sess)
}

// buildDownlinkPkt 造一个「公网 → 客户端」的 UDP 报文（端口非 53，避免被 DNS 规则改道），
// marker 用来区分不同报文内容，便于还原后比对。
func buildDownlinkPkt(src, dst string, marker byte) []byte {
	pkt := buildIPv4(src, dst, marker) // 该函数返回 21 字节，协议号是 ICMP
	pkt[9] = 17                        // 改成 UDP：pick() 才会走 DATAGRAM
	return pkt
}

func waitFrames(conn *fakeConn, want int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if len(conn.sent()) >= want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// P1-7 回归：下行 FEC 编码器必须「每会话一个」。
//
// 修复前 tunLoop 里只有一个全局编码器：A 的一个包 + B 的一个包会凑成同一个 FEC 组，
// 校验帧只发给触发凑满的 B，A 永远缺包；反之 B 收到的校验帧里混着 A 的数据，
// 谁都无法还原 —— FEC 只烧 1/K 带宽、完全失效（解码端本来就是每会话一个解码器）。
func TestDownlinkFECUsesPerSessionEncoder(t *testing.T) {
	const group = 2
	srv, tun := newDownlinkFECTestServer(t, group)
	connA, connB := &fakeConn{}, &fakeConn{}
	registerDownlinkDatagram(srv, "10.88.1.12", connA)
	registerDownlinkDatagram(srv, "10.88.1.13", connB)

	pktA1 := buildDownlinkPkt("1.1.1.1", "10.88.1.12", 0xA1)
	pktA2 := buildDownlinkPkt("1.1.1.1", "10.88.1.12", 0xA2)
	pktB1 := buildDownlinkPkt("1.1.1.1", "10.88.1.13", 0xB1)

	// A 一个包：只应有 1 个数据帧（组没凑满，不该有校验帧）
	tun.ch <- pktA1
	if !waitFrames(connA, 1, 2*time.Second) {
		t.Fatal("A 没收到下行数据帧")
	}
	// B 一个包：如果编码器共享，B 会同时收到「校验帧」（它凑满了 A 的组）
	tun.ch <- pktB1
	if !waitFrames(connB, 1, 2*time.Second) {
		t.Fatal("B 没收到下行数据帧")
	}
	time.Sleep(150 * time.Millisecond)
	if got := len(connA.sent()); got != 1 {
		t.Fatalf("A 收到 %d 帧，期望 1（此时 A 的组只填了 1 个包）", got)
	}
	if got := len(connB.sent()); got != 1 {
		t.Fatalf("B 收到 %d 帧，期望 1：不应收到 A 那组的校验帧（编码器必须每会话独立）", got)
	}

	// A 再来一个包：本会话的组凑满 → A 应收到 数据帧 + 校验帧
	tun.ch <- pktA2
	if !waitFrames(connA, 3, 2*time.Second) {
		t.Fatalf("A 组凑满后应共收到 3 帧（2 数据 + 1 校验），实际 %d", len(connA.sent()))
	}
	if got := len(connB.sent()); got != 1 {
		t.Fatalf("B 的帧数被 A 的流量影响：%d（编码器没有按会话隔离）", got)
	}

	// 用 A 收到的帧模拟客户端解码：丢掉第一个数据帧，应能靠校验帧还原
	frames := connA.sent()
	dec := newFECDecoder()
	var recovered [][]byte
	for i, f := range frames {
		if i == 0 {
			continue
		}
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("第 %d 帧解码失败：%v", i, err)
		}
		recovered = append(recovered, pkts...)
	}
	found := false
	for _, p := range recovered {
		if bytes.Equal(p, pktA1) {
			found = true
		}
	}
	if !found {
		t.Fatal("A 的 FEC 组无法还原被丢的包：校验帧与数据帧不在同一个会话")
	}
	if rec, _ := dec.FECStats(); rec != 1 {
		t.Fatalf("recovered 应为 1，实际 %d", rec)
	}
	// 每会话编码器从 group 0 开始：A 的三帧应属于同一组（group 0）
	for i, f := range frames {
		if len(f) < fecHeaderLen {
			t.Fatalf("第 %d 帧太短：%d", i, len(f))
		}
		if gid := int(f[1])<<8 | int(f[2]); gid != 0 {
			t.Fatalf("第 %d 帧 group=%d，期望 0（每会话独立组号）", i, gid)
		}
	}
}

// FEC 关闭时下行不额外发校验帧（避免回归到「多收包」）。
func TestDownlinkFECDisabledSendsSingleFrame(t *testing.T) {
	srv, tun := newDownlinkFECTestServer(t, 0) // group<=1 视为关闭
	conn := &fakeConn{}
	registerDownlinkDatagram(srv, "10.88.1.12", conn)
	pkt := buildDownlinkPkt("1.1.1.1", "10.88.1.12", 0x01)
	tun.ch <- pkt
	if !waitFrames(conn, 1, 2*time.Second) {
		t.Fatal("关闭 FEC 后仍应正常转发下行包")
	}
	time.Sleep(100 * time.Millisecond)
	if got := len(conn.sent()); got != 1 {
		t.Fatalf("关闭 FEC 时应只发 1 个帧，实际 %d", got)
	}
	if frames := conn.sent(); !bytes.Equal(frames[0], pkt) {
		t.Fatalf("关闭 FEC 时下行应原样转发，实际帧=% x", frames[0])
	}
}
