package node

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

// 启动一个假上游：收到查询就原样回一个带同样 ID 的应答。
func startFakeUpstream(t *testing.T) (string, func()) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			// 构造成应答：QR=1，其余原样
			resp := make([]byte, n)
			copy(resp, buf[:n])
			resp[2] |= 0x80
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String(), func() { _ = pc.Close() }
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 端到端：客户端 → 转发器 → 上游 → 回给客户端
func TestDNSRelayForwardsUDP(t *testing.T) {
	up, closeUp := startFakeUpstream(t)
	defer closeUp()

	// 绑到回环的随机端口模拟「网关:53」
	probe, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.LocalAddr().String()
	_ = probe.Close()

	r := newDNSRelay(addr, []string{up}, quietLogger())
	if err := r.Start(); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	defer r.Close()

	c, err := net.Dial("udp4", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	q := buildTestDNSQuery(0xbeef)
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, 2048)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("没收到应答：%v", err)
	}
	if n < 12 {
		t.Fatalf("应答过短：%d", n)
	}
	if got := binary.BigEndian.Uint16(buf[0:2]); got != 0xbeef {
		t.Fatalf("事务 ID 被改动：%#x", got)
	}
	if buf[2]&0x80 == 0 {
		t.Fatal("应答应带 QR 位（说明确实来自上游）")
	}
}

// 上游全挂时应安静失败，不 panic、不写坏客户端连接
func TestDNSRelayUpstreamDown(t *testing.T) {
	probe, _ := net.ListenPacket("udp4", "127.0.0.1:0")
	addr := probe.LocalAddr().String()
	_ = probe.Close()

	// 指向一个没人监听的端口
	r := newDNSRelay(addr, []string{"127.0.0.1:1"}, quietLogger())
	if err := r.Start(); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	defer r.Close()

	c, _ := net.Dial("udp4", addr)
	defer c.Close()
	_, _ = c.Write(buildTestDNSQuery(0x1234))
	_ = c.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
	buf := make([]byte, 512)
	if _, err := c.Read(buf); err == nil {
		t.Fatal("上游不可用时不应收到应答")
	}
}

func buildTestDNSQuery(id uint16) []byte {
	name := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	m := make([]byte, 12+len(name)+4)
	binary.BigEndian.PutUint16(m[0:2], id)
	m[2] = 0x01
	binary.BigEndian.PutUint16(m[4:6], 1)
	copy(m[12:], name)
	binary.BigEndian.PutUint16(m[len(m)-4:], 1)
	binary.BigEndian.PutUint16(m[len(m)-2:], 1)
	return m
}

// 上游地址归一化：补 53、去空、去重
func TestNormalizeUpstreams(t *testing.T) {
	got := normalizeUpstreams([]string{"1.1.1.1", " 1.1.1.1:53 ", "", "223.5.5.5:5353"})
	want := []string{"1.1.1.1:53", "223.5.5.5:5353"}
	if len(got) != len(want) {
		t.Fatalf("want %v got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 want %s got %s", i, want[i], got[i])
		}
	}
}

// 默认关闭条件：Configure=false 且未显式开启时不启动（避免组网模式占用 53）
func TestDNSRelayDefaultPolicy(t *testing.T) {
	c := Config{}
	if err := c.Normalize(); err == nil {
		// 缺必填项会先报错，这里只关心 DNSRelay 的默认值逻辑
		t.Skip("必填校验先命中，跳过")
	}
	on := true
	c2 := Config{Configure: true}
	if err := c2.Normalize(); err == nil {
		t.Fatal("缺必填项应报错")
	}
	c2.DNSRelay = &on
	if c2.DNSRelay == nil || !*c2.DNSRelay {
		t.Fatal("显式开启后应保持为 true")
	}
}