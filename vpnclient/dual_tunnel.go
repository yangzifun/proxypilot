package main

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
)

// 双通道数据面（36.0）。
//
// 背景：原来所有 IP 包都塞进一条 HTTP/3 可靠流，一个丢包就会让后面所有包
// 排队等重传（队头阻塞）。实测大流量时 DNS 解析慢、视频通话卡、游戏抖动，
// 都是这一个原因。
//
// 分路规则（与节点 sessionSet.pick 保持一致）：
//
//	UDP(17)  → QUIC DATAGRAM：不重传、不排序，丢了只影响它自己
//	TCP(6)   → 可靠流：需要可靠 + 有序
//	ICMP(1)  → 可靠流：ping 要反映真实链路，不能被 DATAGRAM 的丢弃污染
//
// DATAGRAM 不可用（对端没开、或包太大）时**回退到可靠流**，绝不静默丢包。

const (
	// udpProto / tcpProto / icmpProto 是内层 IPv4 头的协议号。
	udpProto  = 17
	tcpProto  = 6
	icmpProto = 1

	// dualQueueDepth 是下行合并队列深度。
	dualQueueDepth = 2048

	// maxDatagramPayload 是单条 DATAGRAM 的载荷上限。
	// 节点侧 max_datagram = MTU+80，内层报文不超过 MTU，留一点余量。
	maxDatagramPayload = 1400
)

// datagramPeer 是 QUIC 连接上需要用到的方法子集（便于测试替身）。
type datagramPeer interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(code uint64, text string) error
	Done() <-chan struct{}
}

// dualPacketConn 把「可靠流」与「QUIC DATAGRAM」合成一个 packetConn。
type dualPacketConn struct {
	stream packetConn
	dg     datagramPeer
	// maxDatagram 是单条 DATAGRAM 的载荷上限，超过就走可靠流。
	maxDatagram int

	q    chan []byte
	done chan struct{}
	once sync.Once

	wg sync.WaitGroup

	errMu   sync.Mutex
	lastErr error

	dropped    atomic.Uint64
	udpSent    atomic.Uint64
	udpRecv    atomic.Uint64
	streamSent atomic.Uint64
	streamRecv atomic.Uint64
}

func newDualPacketConn(stream packetConn, dg datagramPeer, maxDatagram int) *dualPacketConn {
	if maxDatagram <= 0 {
		maxDatagram = 1400
	}
	c := &dualPacketConn{
		stream:      stream,
		dg:          dg,
		maxDatagram: maxDatagram,
		q:           make(chan []byte, dualQueueDepth),
		done:        make(chan struct{}),
	}
	c.wg.Add(2)
	go c.streamLoop()
	go c.datagramLoop()
	return c
}

// innerProto 取内层 IPv4 头的协议号；取不到返回 0。
func innerProto(pkt []byte) byte {
	if len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0
	}
	return pkt[9]
}

// SendPacket 按协议号分流。
func (c *dualPacketConn) SendPacket(pkt []byte) error {
	proto := innerProto(pkt)
	// 只有 UDP 且长度在上限内才走 DATAGRAM
	if proto == udpProto && c.dg != nil && len(pkt) <= c.maxDatagram {
		if err := c.dg.SendDatagram(pkt); err == nil {
			c.udpSent.Add(1)
			return nil
		}
		// DATAGRAM 发不出去（对端不支持 / 缓冲满）时回退到可靠流
		log.Printf("数据面：DATAGRAM 发送失败，回退到可靠流（%v）", errors.New("send failed"))
	}
	if err := c.stream.SendPacket(pkt); err != nil {
		return err
	}
	c.streamSent.Add(1)
	return nil
}

// streamLoop 把可靠流上的下行包搬进合并队列。
func (c *dualPacketConn) streamLoop() {
	defer c.wg.Done()
	for {
		pkt, err := c.stream.RecvPacket()
		if err != nil {
			c.fail(err)
			return
		}
		c.streamRecv.Add(1)
		if !c.push(pkt) {
			return
		}
	}
}

// datagramLoop 把 DATAGRAM 上的下行包搬进合并队列。
func (c *dualPacketConn) datagramLoop() {
	defer c.wg.Done()
	if c.dg == nil {
		return
	}
	for {
		pkt, err := c.dg.ReceiveDatagram(context.Background())
		if err != nil {
			// DATAGRAM 断开不应直接判定整条隧道失败：可靠流可能还在。
			// 交给 streamLoop 决定隧道生死。
			if c.isDone() {
				return
			}
			log.Printf("数据面：DATAGRAM 接收结束（%v），UDP 流量回退到可靠流", err)
			return
		}
		c.udpRecv.Add(1)
		if !c.push(pkt) {
			return
		}
	}
}

func (c *dualPacketConn) push(pkt []byte) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.q <- pkt:
		return true
	case <-c.done:
		return false
	default:
		// 队列满：丢掉并计数（与可靠流侧一致）
		c.dropped.Add(1)
		return true
	}
}

func (c *dualPacketConn) isDone() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *dualPacketConn) fail(err error) {
	c.once.Do(func() {
		c.errMu.Lock()
		c.lastErr = err
		c.errMu.Unlock()
		close(c.done)
	})
}

func (c *dualPacketConn) lastError() error {
	c.errMu.Lock()
	defer c.errMu.Unlock()
	if c.lastErr == nil {
		return errors.New("隧道已关闭")
	}
	return c.lastErr
}

// RecvPacket 从合并队列取一个下行包。
func (c *dualPacketConn) RecvPacket() ([]byte, error) {
	select {
	case pkt := <-c.q:
		return pkt, nil
	case <-c.done:
		// 关停前把队列里剩下的包发完
		select {
		case pkt := <-c.q:
			return pkt, nil
		default:
			return nil, c.lastError()
		}
	}
}

func (c *dualPacketConn) Close() error {
	c.fail(errors.New("隧道已关闭"))
	c.wg.Wait()
	if c.dg != nil {
		_ = c.dg.CloseWithError(0, "tunnel closed")
	}
	return c.stream.Close()
}

// DropStats 合并队列溢出数（用于诊断）。
func (c *dualPacketConn) DropStats() uint64 { return c.dropped.Load() }

// PathStats 返回两条通道各自的收发计数（诊断用）。
func (c *dualPacketConn) PathStats() (udpSent, udpRecv, streamSent, streamRecv uint64) {
	return c.udpSent.Load(), c.udpRecv.Load(), c.streamSent.Load(), c.streamRecv.Load()
}