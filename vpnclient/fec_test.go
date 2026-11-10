package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func makePayload(size int, seed byte) []byte {
	p := make([]byte, size)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:4], uint16(size))
	p[9] = 17
	for i := 4; i < size; i++ {
		p[i] = seed + byte(i)
	}
	return p
}

// 组内不丢包：全部原样还原
func TestFECNoLoss(t *testing.T) {
	enc := newFECEncoder(4)
	dec := newFECDecoder()

	var frames [][]byte
	for i := 0; i < 4; i++ {
		d, p := enc.Encode(makePayload(60, byte(i)))
		frames = append(frames, d)
		if p != nil {
			frames = append(frames, p)
		}
	}
	if len(frames) != 5 {
		t.Fatalf("4 个包应产出 4 数据帧 + 1 校验帧，实际 %d", len(frames))
	}

	var out [][]byte
	for _, f := range frames {
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		out = append(out, pkts...)
	}
	if len(out) != 4 {
		t.Fatalf("应还原 4 个包，实际 %d", len(out))
	}
	for i, p := range out {
		if !bytes.Equal(p, makePayload(60, byte(i))) {
			t.Fatalf("第 %d 个包内容不符", i)
		}
	}
}

// 组内丢 1 个：必须被校验帧还原，且长度正确
func TestFECRecoverSingleLoss(t *testing.T) {
	enc := newFECEncoder(4)
	dec := newFECDecoder()

	payloads := [][]byte{
		makePayload(60, 1),
		makePayload(80, 2),
		makePayload(100, 3),
		makePayload(40, 4),
	}

	var frames [][]byte
	for _, p := range payloads {
		d, par := enc.Encode(p)
		frames = append(frames, d)
		if par != nil {
			frames = append(frames, par)
		}
	}

	// 丢掉第 2 个数据帧（index=1）
	const drop = 1
	var out [][]byte
	for i, f := range frames {
		if i == drop {
			continue
		}
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		out = append(out, pkts...)
	}

	if len(out) != 4 {
		t.Fatalf("丢 1 个应被还原为 4 个，实际 %d", len(out))
	}
	// 还原出来的包是"后补"的，会排在末尾 —— UDP 不要求顺序，这里按内容比对
	found := false
	for _, p := range out {
		if bytes.Equal(p, payloads[1]) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("还原出来的包内容不符")
	}
	if rec, _ := dec.FECStats(); rec != 1 {
		t.Fatalf("recovered 应为 1，实际 %d", rec)
	}
}

// 组内丢 2 个：无法还原，不应产出错误数据
func TestFECCannotRecoverTwoLosses(t *testing.T) {
	enc := newFECEncoder(4)
	dec := newFECDecoder()

	var frames [][]byte
	for i := 0; i < 4; i++ {
		d, p := enc.Encode(makePayload(60, byte(i)))
		frames = append(frames, d)
		if p != nil {
			frames = append(frames, p)
		}
	}

	var out [][]byte
	for i, f := range frames {
		if i == 0 || i == 2 { // 丢两个数据帧
			continue
		}
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		out = append(out, pkts...)
	}
	// 只剩 2 个原始包，校验帧放弃
	if len(out) != 2 {
		t.Fatalf("丢 2 个只应保留 2 个，实际 %d", len(out))
	}
	if _, dropped := dec.FECStats(); dropped != 1 {
		t.Fatalf("dropped 应为 1，实际 %d", dropped)
	}
}

// 校验帧先于部分数据帧到达（乱序）时不应误判
func TestFECOutOfOrder(t *testing.T) {
	enc := newFECEncoder(4)
	dec := newFECDecoder()

	var frames [][]byte
	for i := 0; i < 4; i++ {
		d, p := enc.Encode(makePayload(60, byte(i)))
		frames = append(frames, d)
		if p != nil {
			frames = append(frames, p)
		}
	}
	// 校验帧移到最前：此时 4 个数据包都还没到，应判「无法还原」而不是崩
	var out [][]byte
	pkts, err := dec.Decode(frames[len(frames)-1])
	if err != nil {
		t.Fatalf("乱序解码失败：%v", err)
	}
	out = append(out, pkts...)
	for i := 0; i < 4; i++ {
		p, err := dec.Decode(frames[i])
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		out = append(out, p...)
	}
	if len(out) != 4 {
		t.Fatalf("乱序后仍应拿到 4 个包，实际 %d", len(out))
	}
}

// Flush 让未凑满的组也能产出校验帧
func TestFECFlushPartialGroup(t *testing.T) {
	enc := newFECEncoder(8)
	dec := newFECDecoder()

	var frames [][]byte
	for i := 0; i < 3; i++ {
		d, p := enc.Encode(makePayload(50, byte(i)))
		frames = append(frames, d)
		if p != nil {
			frames = append(frames, p)
		}
	}
	if par := enc.Flush(); par != nil {
		frames = append(frames, par)
	} else {
		t.Fatal("未凑满的组 Flush 应产出校验帧")
	}

	// 丢第 1 个，靠 Flush 出来的校验帧还原
	var out [][]byte
	for i, f := range frames {
		if i == 1 {
			continue
		}
		pkts, err := dec.Decode(f)
		if err != nil {
			t.Fatalf("解码失败：%v", err)
		}
		out = append(out, pkts...)
	}
	if len(out) != 3 {
		t.Fatalf("应还原 3 个包，实际 %d", len(out))
	}
}