package main

import (
	"encoding/binary"
	"testing"
)

// 连通性探测必须发送「纯 DNS 消息」，不能再把手工拼的 IPv4 包当成 UDP 载荷
// （历史 bug：对端收到乱码，客户端永远显示「连通检测 不通」）。
func TestBuildDNSMessageIsPlainDNS(t *testing.T) {
	msg := buildDNSMessage(0x5a5a)
	if len(msg) < 12 {
		t.Fatalf("报文太短：%d", len(msg))
	}
	if got := binary.BigEndian.Uint16(msg[0:2]); got != 0x5a5a {
		t.Fatalf("事务 ID = %#x，期望 0x5a5a", got)
	}
	if msg[2] != 0x01 {
		t.Fatalf("flags 期望 RD=1，实际 %#x", msg[2])
	}
	if got := binary.BigEndian.Uint16(msg[4:6]); got != 1 {
		t.Fatalf("QDCOUNT = %d，期望 1", got)
	}
	if msg[0]>>4 == 4 {
		t.Fatal("报文以 IPv4 版本号开头，说明仍在伪造 IP 头")
	}
	if got := binary.BigEndian.Uint16(msg[len(msg)-4:]); got != 1 {
		t.Fatalf("QTYPE = %d，期望 1(A)", got)
	}
	if got := binary.BigEndian.Uint16(msg[len(msg)-2:]); got != 1 {
		t.Fatalf("QCLASS = %d，期望 1(IN)", got)
	}
	if got := string(msg[12:20]); got != "\x07example" {
		t.Fatalf("查询域名异常：%q", got)
	}
}
