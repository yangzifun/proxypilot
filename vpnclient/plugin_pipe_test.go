package main

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestPluginFrameRoundTrip(t *testing.T) {
	for _, size := range []int{1, 20, 40, 1380, 1500, 9000, pluginFrameMaxLen} {
		pkt := bytes.Repeat([]byte{0xA5}, size)
		frame, err := encodePluginFrame(pkt)
		if err != nil {
			t.Fatalf("size=%d 编码失败：%v", size, err)
		}
		if len(frame) != size+pluginFrameHdrLen {
			t.Fatalf("size=%d 帧长 %d，期望 %d", size, len(frame), size+pluginFrameHdrLen)
		}
		got, err := readPluginFrame(bytes.NewReader(frame), nil)
		if err != nil {
			t.Fatalf("size=%d 解码失败：%v", size, err)
		}
		if !bytes.Equal(got, pkt) {
			t.Fatalf("size=%d 内容不一致", size)
		}
	}
}

// 长度字段必须是大端，插件侧容易写成小端，这个用例把它钉死。
func TestPluginFrameBigEndianLength(t *testing.T) {
	pkt := bytes.Repeat([]byte{0x11}, 0x0102) // 258
	frame, err := encodePluginFrame(pkt)
	if err != nil {
		t.Fatal(err)
	}
	if frame[0] != 0x01 || frame[1] != 0x02 {
		t.Fatalf("长度字节应为 01 02（大端），实际 %02x %02x", frame[0], frame[1])
	}
}

func TestPluginFrameRejectsEmpty(t *testing.T) {
	if _, err := encodePluginFrame(nil); err == nil {
		t.Fatal("空包应被拒绝")
	}
	// 长度为 0 的帧也必须报错，否则流会错位
	if _, err := readPluginFrame(bytes.NewReader([]byte{0x00, 0x00}), nil); !errors.Is(err, errPluginFrameTooShort) {
		t.Fatalf("空帧应返回 errPluginFrameTooShort，实际 %v", err)
	}
}

func TestPluginFrameRejectsOversize(t *testing.T) {
	big := bytes.Repeat([]byte{1}, pluginFrameMaxLen+1)
	if _, err := encodePluginFrame(big); err == nil {
		t.Fatal("超过上限应被拒绝")
	}
}

// 连续多帧要能按顺序读出来（隧道里就是持续不断的包）
func TestPluginFrameMultipleInStream(t *testing.T) {
	var buf bytes.Buffer
	pkts := [][]byte{
		{1, 2, 3},
		bytes.Repeat([]byte{7}, 1380),
		{9},
	}
	for _, p := range pkts {
		if err := writePluginFrame(&buf, p); err != nil {
			t.Fatal(err)
		}
	}
	r := bytes.NewReader(buf.Bytes())
	var scratch []byte
	for i, want := range pkts {
		got, err := readPluginFrame(r, scratch)
		if err != nil {
			t.Fatalf("第 %d 帧读取失败：%v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("第 %d 帧内容不一致", i)
		}
		scratch = got
	}
	if _, err := readPluginFrame(r, scratch); err != io.EOF {
		t.Fatalf("读完后应返回 EOF，实际 %v", err)
	}
}

// 半帧（长度写了但包体没发完）必须报错而不是返回短包
func TestPluginFrameTruncatedPayload(t *testing.T) {
	if _, err := readPluginFrame(bytes.NewReader([]byte{0x00, 0x10, 0x01, 0x02}), nil); err == nil {
		t.Fatal("包体不足应报错")
	}
}