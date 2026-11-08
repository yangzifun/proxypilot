package node

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestStripPaddingIPv4(t *testing.T) {
	src := net.IPv4(10, 88, 7, 23).To4()
	dst := net.IPv4(1, 1, 1, 1).To4()
	pkt := make([]byte, 28)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], 28)
	copy(pkt[12:16], src)
	copy(pkt[16:20], dst)
	raw := append(append([]byte(nil), pkt...), bytes.Repeat([]byte{0xaa}, 40)...)

	got, s, d, err := StripPadding(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, pkt) {
		t.Fatalf("pkt = %d 字节，期望 %d", len(got), len(pkt))
	}
	if !s.Equal(src) || !d.Equal(dst) {
		t.Fatalf("src/dst = %v/%v", s, d)
	}
}

func TestStripPaddingIPv6(t *testing.T) {
	src := net.ParseIP("fd00:88::17")
	dst := net.ParseIP("2606:4700:4700::1111")
	pkt := make([]byte, 44)
	pkt[0] = 0x60
	binary.BigEndian.PutUint16(pkt[4:6], 4)
	copy(pkt[8:24], src.To16())
	copy(pkt[24:40], dst.To16())
	raw := append(append([]byte(nil), pkt...), 0x00, 0x01)

	got, s, d, err := StripPadding(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 44 {
		t.Fatalf("len = %d", len(got))
	}
	if !s.Equal(src) || !d.Equal(dst) {
		t.Fatalf("src/dst = %v/%v", s, d)
	}
}

func TestStripPaddingBad(t *testing.T) {
	if _, _, _, err := StripPadding([]byte{0x45}); err == nil {
		t.Fatal("短报文未被拒绝")
	}
	if _, _, _, err := StripPadding([]byte{0x99, 0, 0, 0}); err == nil {
		t.Fatal("未知版本未被拒绝")
	}
}
