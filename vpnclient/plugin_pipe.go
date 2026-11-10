package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// UWP VPN 插件 ↔ Go 核心的包搬运协议。
//
// 插件侧（C#，Windows.Networking.Vpn）只负责把 VpnChannel 里的明文 IP 包
// 原样送过来；加密与隧道都在 Go 这边。所以这里只需要一个极小的分帧格式：
//
//	+--------+--------+------------------+
//	| len_hi | len_lo | payload (IP 包)  |
//	+--------+--------+------------------+
//	     2 字节大端        len 字节
//
// 设计约束：
//   - 单帧最大 65535（IPv4 包最大也就 65535，MTU 1380 时远小于此）
//   - 读到 EOF 表示插件断开（用户点了系统 VPN 的「断开」）
//   - 长度非法必须立刻报错，不能继续读 —— 否则会把后面的字节当长度，整条流错位

const (
	pluginPipeName    = `\\.\pipe\proxypilot-vpn`
	pluginFrameMaxLen = 65535
	pluginFrameHdrLen = 2
)

// errPluginFrameTooShort 说明对端发来了空帧（长度为 0），视为协议错误。
var errPluginFrameTooShort = errors.New("插件管道：收到空帧")

// encodePluginFrame 把单个 IP 包编码成一帧。
func encodePluginFrame(pkt []byte) ([]byte, error) {
	if len(pkt) == 0 {
		return nil, fmt.Errorf("空包")
	}
	if len(pkt) > pluginFrameMaxLen {
		return nil, fmt.Errorf("包过大：%d 字节（上限 %d）", len(pkt), pluginFrameMaxLen)
	}
	out := make([]byte, pluginFrameHdrLen+len(pkt))
	binary.BigEndian.PutUint16(out[0:2], uint16(len(pkt)))
	copy(out[pluginFrameHdrLen:], pkt)
	return out, nil
}

// readPluginFrame 从流里读一帧，返回包体。
// 读到的包体会复用传入的 buf（若容量足够），避免每包一次分配。
func readPluginFrame(r io.Reader, buf []byte) ([]byte, error) {
	var hdr [pluginFrameHdrLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 {
		return nil, errPluginFrameTooShort
	}
	if n > pluginFrameMaxLen {
		return nil, fmt.Errorf("帧长度非法：%d", n)
	}
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// writePluginFrame 写一帧（一次性 write，避免插件侧读到半帧长度）。
func writePluginFrame(w io.Writer, pkt []byte) error {
	frame, err := encodePluginFrame(pkt)
	if err != nil {
		return err
	}
	_, err = w.Write(frame)
	return err
}