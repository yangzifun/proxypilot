package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"sync"
	"sync/atomic"
	"time"
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

// 发送侧批量聚合参数。
//
// 逐个报文 Write 会为每个包生成一个 QUIC STREAM 帧与一次 sentinel 唤醒，
// 小包场景（TCP ACK、游戏、DNS）下开销是有效载荷的数倍，实测 pps 上不去；
// 更重要的是写完就阻塞在流控上会让网卡读协程停摆，Wintun 环形缓冲溢出，
// 表现为「连上就丢包」。这里改成「有界队列 + 聚合写」，读协程永不阻塞。
const (
	maxFrame       = 4096        // 单帧上限，防御异常长度
	sendQueueDepth = 2048        // 队列深度（约 2.8MB），吸收突发
	batchMaxBytes  = 64 * 1024   // 单次写入的聚合上限
	batchMaxDelay  = 2 * time.Millisecond
)

// streamPacketConn 把一条双向 HTTP/3 流包装成报文通道。
type streamPacketConn struct {
	w io.Writer
	r io.Reader
	// br 缓冲读，只在 RecvPacket（单协程）里使用
	br *bufio.Reader

	q         chan []byte
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	closed    atomic.Bool
	dropped   atomic.Uint64
	sent      atomic.Uint64
	loggedOK  atomic.Bool
	loggedErr atomic.Bool
}

func newStreamPacketConn(w io.Writer, r io.Reader) *streamPacketConn {
	c := &streamPacketConn{
		w:    w,
		r:    r,
		q:    make(chan []byte, sendQueueDepth),
		done: make(chan struct{}),
	}
	c.wg.Add(1)
	go c.writeLoop()
	return c
}

// SendPacket 只做入队，不阻塞读网卡的协程。
//
// 队列满时丢弃并计数（而不是返回错误）：返回错误会让上层把整条隧道判死，
// 这对 VPN 是不可接受的 —— 宁可丢一个包，也要让 TCP 自己重传。
func (c *streamPacketConn) SendPacket(pkt []byte) error {
	if len(pkt) == 0 || len(pkt) > maxFrame {
		return fmt.Errorf("报文长度非法：%d", len(pkt))
	}
	if c.closed.Load() {
		return fmt.Errorf("流已关闭")
	}
	buf := make([]byte, 2+len(pkt))
	binary.BigEndian.PutUint16(buf, uint16(len(pkt)))
	copy(buf[2:], pkt)

	select {
	case c.q <- buf:
		return nil
	default:
		if c.dropped.Add(1)%1000 == 1 {
			log.Printf("数据面：发送队列已满，累计丢弃 %d 个报文（隧道带宽不足）", c.dropped.Load())
		}
		return nil
	}
}

// writeLoop 从队列取报文，按 [2B len][pkt] 拼接后成批写入流。
func (c *streamPacketConn) writeLoop() {
	defer c.wg.Done()
	buf := make([]byte, 0, batchMaxBytes)

	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		_, err := c.w.Write(buf)
		buf = buf[:0]
		return err
	}

	for {
		select {
		case <-c.done:
			_ = flush()
			return
		case pkt := <-c.q:
			buf = appendFrame(buf, pkt)

			// 贪心聚合：把队列里已经就绪的报文一起打包，减少 QUIC 帧数量。
			for len(buf) < batchMaxBytes {
				select {
				case p2 := <-c.q:
					buf = appendFrame(buf, p2)
					continue
				default:
				}
				break
			}

			// 队列已空且包数不多时，短暂等一下，让同一批上行流量合并成一次写。
			if len(buf) < batchMaxBytes/4 {
				timer := time.NewTimer(batchMaxDelay)
			drain:
				for len(buf) < batchMaxBytes {
					select {
					case p2 := <-c.q:
						buf = appendFrame(buf, p2)
					case <-timer.C:
						break drain
					case <-c.done:
						timer.Stop()
						_ = flush()
						return
					}
				}
				timer.Stop()
			}

			if err := flush(); err != nil {
				if c.loggedErr.CompareAndSwap(false, true) {
					log.Printf("数据面：写入隧道流失败：%v", err)
				}
				return
			}
			c.sent.Add(1)
			if c.loggedOK.CompareAndSwap(false, true) {
				log.Printf("数据面：首个报文已写入隧道流（聚合 %d 字节）", len(pkt))
			}
		}
	}
}

func appendFrame(dst, pkt []byte) []byte {
	var hdr [2]byte
	binary.BigEndian.PutUint16(hdr[:], uint16(len(pkt)))
	dst = append(dst, hdr[:]...)
	return append(dst, pkt...)
}

// RecvPacket 用 bufio 缓冲读取，减少每包两次 ReadFull 带来的系统调用。
func (c *streamPacketConn) RecvPacket() ([]byte, error) {
	if c.br == nil {
		c.br = bufio.NewReaderSize(c.r, 64*1024)
	}
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[:]))
	if n == 0 || n > maxFrame {
		return nil, fmt.Errorf("帧长度非法：%d", n)
	}
	pkt := make([]byte, n)
	if _, err := io.ReadFull(c.br, pkt); err != nil {
		return nil, err
	}
	return pkt, nil
}

func (c *streamPacketConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		close(c.done)
		c.wg.Wait()
		if cw, ok := c.w.(io.Closer); ok {
			err = cw.Close()
		}
	})
	return err
}

// DropStats 返回因队列满而丢弃的报文数（用于诊断）。
func (c *streamPacketConn) DropStats() uint64 { return c.dropped.Load() }