package main

import (
	"bytes"
	"testing"
	"time"
)

// FEC 关闭时：UDP 包原样进入 DATAGRAM，不多发校验帧。
func TestDualFECDisabledPassthrough(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 1400, 0)

	p := makeIPPacket(udpProto, 60)
	if err := c.SendPacket(p); err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if dg.sentCount() != 1 {
		t.Fatalf("关闭 FEC 时只应发 1 个 DATAGRAM，实际 %d", dg.sentCount())
	}
}

// 开启 FEC 时：一组 4 个包应产出 4 数据帧 + 1 校验帧，
// 且组内丢 1 个能被还原。
func TestDualFECSendAndRecover(t *testing.T) {
	st := newFakeStream()
	dg := newFakeDatagram()
	c := newDualPacketConn(st, dg, 1400, 4)

	payloads := [][]byte{
		makeIPPacket(udpProto, 60),
		makeIPPacket(udpProto, 80),
		makeIPPacket(udpProto, 100),
		makeIPPacket(udpProto, 40),
	}
	for i, p := range payloads {
		p[8] = byte(0x80 + i) // TTL 位当标记，方便后面区分
		if err := c.SendPacket(p); err != nil {
			t.Fatalf("第 %d 个发送失败：%v", i, err)
		}
	}

	// 4 个数据帧 + 1 个校验帧
	if dg.sentCount() != 5 {
		t.Fatalf("K=4 时应发 4 数据帧 + 1 校验帧，实际 %d", dg.sentCount())
	}

	// 模拟接收端：丢掉第 2 个数据帧，其余（含校验帧）灌进去
	dg.mu.Lock()
	frames := append([][]byte{}, dg.sent...)
	dg.mu.Unlock()

	for i, f := range frames {
		if i == 1 {
			continue
		}
		dg.recv <- f
	}

	got := map[byte][]byte{}
	deadline := time.After(2 * time.Second)
	for len(got) < 4 {
		select {
		case pkt := <-c.q:
			got[pkt[8]] = pkt
		case <-deadline:
			t.Fatalf("只收到 %d 个包，FEC 未还原", len(got))
		}
	}

	// 第 2 个包（标记 0x81）应被还原且内容一致
	want := payloads[1]
	if !bytes.Equal(got[0x81], want) {
		t.Fatalf("还原包内容不符：% x vs % x", got[0x81], want)
	}
	if rec, _ := c.FECStats(); rec != 1 {
		t.Fatalf("recovered 应为 1，实际 %d", rec)
	}
}