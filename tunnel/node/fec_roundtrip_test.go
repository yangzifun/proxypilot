package node

import (
	"bytes"
	"testing"
)

// 节点侧 FEC 编解码往返：上行解码、下行编码用的是同一套格式，
// 这里验证「对端编码 → 本端解码」能还原出原始 IP 报文。
func TestFECEncodeDecodeRoundTrip(t *testing.T) {
	enc := newFECEncoder(4)
	dec := newFECDecoder()

	payloads := [][]byte{
		make([]byte, 60), make([]byte, 80), make([]byte, 100), make([]byte, 40),
	}
	for i := range payloads {
		payloads[i][0] = 0x45
		payloads[i][9] = 17
		for j := 20; j < len(payloads[i]); j++ {
			payloads[i][j] = byte(i*31 + j)
		}
	}

	var frames [][]byte
	for _, p := range payloads {
		d, parity := enc.Encode(p)
		frames = append(frames, d)
		if len(parity) > 0 {
			frames = append(frames, parity)
		}
	}
	if len(frames) != 5 {
		t.Fatalf("K=4 应产出 4 数据帧 + 1 校验帧，实际 %d", len(frames))
	}

	// 丢第 2 个数据帧，靠校验帧还原
	var got [][]byte
	for i, f := range frames {
		if i == 1 {
			continue
		}
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		got = append(got, pkts...)
	}
	if len(got) != 4 {
		t.Fatalf("应还原 4 个报文，实际 %d", len(got))
	}
	found := false
	for _, p := range got {
		if bytes.Equal(p, payloads[1]) {
			found = true
		}
	}
	if !found {
		t.Fatal("还原出的报文内容与原报文不一致")
	}
	if rec, _ := dec.FECStats(); rec != 1 {
		t.Fatalf("recovered 应为 1，实际 %d", rec)
	}
}