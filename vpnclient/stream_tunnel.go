package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"
)

// 可靠隧道：在 HTTP/3 流上用「2 字节长度 + IP 报文」分帧传输。
//
// 为什么不用 QUIC DATAGRAM：DATAGRAM 不重传，跨境链路一旦丢包就是永久丢失；
// 实测出现「握手正常、探测偶发通过、持续流量 100% 丢」的情况。
// 走 QUIC 流可以享受重传与流控，代价是头部阻塞，但对 VPN 场景可接受。

type packetConn interface {
	SendPacket([]byte) error
	RecvPacket() ([]byte, error)
	Close() error
}

// streamPacketConn 把一条双向 HTTP/3 流包装成报文通道。
type streamPacketConn struct {
	w      io.Writer
	r      io.Reader
	wmu    sync.Mutex
	closed bool
}

func newStreamPacketConn(w io.Writer, r io.Reader) *streamPacketConn {
	return &streamPacketConn{w: w, r: r}
}

// maxFrame 是单帧上限，防御异常长度。
const maxFrame = 4096

func (c *streamPacketConn) SendPacket(pkt []byte) error {
	if len(pkt) == 0 || len(pkt) > maxFrame {
		return fmt.Errorf("报文长度非法：%d", len(pkt))
	}
	buf := make([]byte, 2+len(pkt))
	binary.BigEndian.PutUint16(buf, uint16(len(pkt)))
	copy(buf[2:], pkt)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return fmt.Errorf("流已关闭")
	}
	_, err := c.w.Write(buf)
	return err
}

func (c *streamPacketConn) RecvPacket() ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.r, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 || n > maxFrame {
		return nil, fmt.Errorf("帧长度非法：%d", n)
	}
	pkt := make([]byte, n)
	if _, err := io.ReadFull(c.r, pkt); err != nil {
		return nil, err
	}
	return pkt, nil
}

func (c *streamPacketConn) Close() error {
	c.wmu.Lock()
	c.closed = true
	c.wmu.Unlock()
	if cw, ok := c.w.(io.Closer); ok {
		return cw.Close()
	}
	return nil
}