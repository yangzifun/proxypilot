package main

import (
	"encoding/binary"
	"testing"
)

// 造一个 IPv4 报文（UDP 或 TCP，头部长度 20，total 由 payload 决定）。
func makeIPv4(payload int, src, dst byte) []byte {
	total := 20 + payload
	p := make([]byte, total)
	p[0] = 0x45 // version 4, ihl 5
	binary.BigEndian.PutUint16(p[2:4], uint16(total))
	p[8] = 64   // ttl
	p[9] = 17   // udp
	p[12] = 10
	p[13] = 88
	p[14] = 3
	p[15] = src
	p[16] = 172
	p[17] = 67
	p[18] = 138
	p[19] = dst
	return p
}

// 核心回归：Windows 卸递交上来的「多段合体包」必须被切成多个独立报文。
//
// 真实样本（客户端日志）：
//   帧 1025 字节，IP 头声明 40 字节  → 一个 40 字节 ACK + 后面还有 985 字节
//   帧 220 字节，IP 头声明 52 字节   → 一个 52 字节 SYN + 168 字节其它健全?
func TestSendRawIPBufferSplitsConcatenatedPackets(t *testing.T) {
	tn := &Tunnel{}
	var got [][]byte
	tn.conn = &collectConn{onSend: func(p []byte) error {
		cp := make([]byte, len(p))
		copy(cp, p)
		got = append(got, cp)
		return nil
	}}

	a := makeIPv4(20, 10, 1)
	b := makeIPv4(30, 10, 2)
	c := makeIPv4(40, 10, 3)
	raw := append(append(append([]byte{}, a...), b...), c...)

	sent, bad, leftover := tn.sendRawIPBuffer(raw)
	if sent != 3 {
		t.Fatalf("应切出 3 个报文，实际 %d（bad=%d leftover=%d）", sent, bad, leftover)
	}
	if bad != 0 || leftover != 0 {
		t.Fatalf("不应有异常：bad=%d leftover=%d", bad, leftover)
	}
	for i, want := range [][]byte{a, b, c} {
		if len(got[i]) != len(want) {
			t.Fatalf("第 %d 个长度不符：%d vs %d", i, len(got[i]), len(want))
		}
	}
}

// 单个报文必须原样发出
func TestSendRawIPBufferSingle(t *testing.T) {
	tn := &Tunnel{}
	var got [][]byte
	tn.conn = &collectConn{onSend: func(p []byte) error {
		cp := make([]byte, len(p))
		copy(cp, p)
		got = append(got, cp)
		return nil
	}}
	pkt := makeIPv4(100, 10, 9)
	sent, bad, leftover := tn.sendRawIPBuffer(pkt)
	if sent != 1 || bad != 0 || leftover != 0 {
		t.Fatalf("单包处理不对：sent=%d bad=%d leftover=%d", sent, bad, leftover)
	}
	if len(got) != 1 || len(got[0]) != len(pkt) {
		t.Fatalf("发出的报文不符")
	}
}

// 截断包（声明长度 > 实有字节）必须判异常，不能发出去
func TestSendRawIPBufferTruncated(t *testing.T) {
	tn := &Tunnel{}
	tn.conn = &collectConn{onSend: func(p []byte) error { return nil }}
	pkt := makeIPv4(100, 10, 9)
	short := pkt[:40] // 只留 40 字节，头里仍声明 120
	sent, bad, _ := tn.sendRawIPBuffer(short)
	if sent != 0 {
		t.Fatalf("截断包不应发出，实际 sent=%d", sent)
	}
	if bad == 0 {
		t.Fatal("截断包应计入 bad")
	}
}

// 尾部凑不成完整报文时，已有部分照发，剩余记为 leftover
func TestSendRawIPBufferPartialTail(t *testing.T) {
	tn := &Tunnel{}
	var got [][]byte
	tn.conn = &collectConn{onSend: func(p []byte) error {
		cp := make([]byte, len(p))
		copy(cp, p)
		got = append(got, cp)
		return nil
	}}
	a := makeIPv4(20, 10, 1)
	raw := append(append([]byte{}, a...), []byte{0x45, 0x00, 0x00}...) // 3 字节残渣
	sent, _, leftover := tn.sendRawIPBuffer(raw)
	if sent != 1 {
		t.Fatalf("第一个包应发出，实际 sent=%d", sent)
	}
	if leftover == 0 {
		t.Fatal("尾部残渣应计入 leftover")
	}
}

// IPv6 报文（本路径不支持）应被整体丢弃，不应误当成 IPv4
func TestSendRawIPBufferRejectsIPv6(t *testing.T) {
	tn := &Tunnel{}
	tn.conn = &collectConn{onSend: func(p []byte) error { return nil }}
	v6 := make([]byte, 40)
	v6[0] = 0x60
	sent, _, leftover := tn.sendRawIPBuffer(v6)
	if sent != 0 {
		t.Fatal("IPv6 不应被发出")
	}
	if leftover != 40 {
		t.Fatalf("IPv6 应整体计入 leftover，实际 %d", leftover)
	}
}

type collectConn struct {
	onSend func([]byte) error
}

func (c *collectConn) SendPacket(p []byte) error          { return c.onSend(p) }
func (c *collectConn) RecvPacket() ([]byte, error)        { return nil, nil }
func (c *collectConn) Close() error                       { return nil }
func (c *collectConn) DropStats() uint64                  { return 0 }