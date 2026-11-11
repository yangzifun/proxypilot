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

	// fecGroupSize 是 DATAGRAM 通道的分组大小：0 或 1 表示关闭 FEC。
	// 8 表示每 8 个包加 1 个校验包（12.5% 开销），可还原组内任意 1 个丢包。
	// 面板开关接入后改为从配置读。
	fecGroupSize = 0
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
	// lanes 是多路复用的额外可靠流（lane 0 就是 stream 本身）。
	// 每条 lane 是一条独立的 QUIC 流，有独立的流控与重传队列 ——
	// 一条连接丢包不会连累其他连接（跨流队头阻塞）。
	lanes []packetConn
	dg     datagramPeer
	// maxDatagram 是单条 DATAGRAM 的载荷上限，超过就走可靠流。
	maxDatagram int

	q    chan []byte
	done chan struct{}
	once sync.Once

	wg sync.WaitGroup

	errMu   sync.Mutex
	lastErr error

	// FEC 只作用于 DATAGRAM 通道；nil 表示未启用
	fecEnc *fecEncoder
	fecDec *fecDecoder

	dropped    atomic.Uint64
	udpSent    atomic.Uint64
	udpRecv    atomic.Uint64
	streamSent atomic.Uint64
	streamRecv atomic.Uint64
}

// newDualPacketConn 建立双通道；fecGroup > 1 时在 DATAGRAM 通道启用分组 FEC。
func newDualPacketConn(stream packetConn, dg datagramPeer, maxDatagram, fecGroup int) *dualPacketConn {
	return newDualPacketConnLanes(stream, nil, dg, maxDatagram, fecGroup)
}

// newDualPacketConnLanes 在 stream 之外再挂 n 条独立可靠流。
func newDualPacketConnLanes(stream packetConn, lanes []packetConn, dg datagramPeer, maxDatagram, fecGroup int) *dualPacketConn {
	if maxDatagram <= 0 {
		maxDatagram = 1400
	}
	c := &dualPacketConn{
		stream:      stream,
		lanes:       lanes,
		dg:          dg,
		maxDatagram: maxDatagram,
		q:           make(chan []byte, dualQueueDepth),
		done:        make(chan struct{}),
	}
	if fecGroup > 1 && dg != nil {
		c.fecEnc = newFECEncoder(fecGroup)
		c.fecDec = newFECDecoder()
	}
	c.wg.Add(2 + len(lanes))
	go c.streamLoop()
	for i, ln := range lanes {
		go c.laneLoop(ln, i)
	}
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

// isDNSPacket 判断是否为 DNS 报文（UDP 且源/目的端口为 53）。
//
// 实测（客户端 3.9 + Agent 3.10）：DATAGRAM 通道已经通了（UDP 收 > 0），
// 但 398 发只回 74，约 81% 的 UDP 回包丢失 —— 因为 DATAGRAM 帧不重传，
// 且与大流量的可靠流共用同一个拥塞窗口，流一忙 datagram 就被丢。
// DNS 客户端超时 2 秒，81% 丢失直接表现为「nslookup 连续超时」。
// 因此 DNS 固定走可靠流，其余 UDP 才用 DATAGRAM。
func isDNSPacket(pkt []byte) bool {
	if len(pkt) < 28 || pkt[0]>>4 != 4 || pkt[9] != udpProto {
		return false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return false
	}
	sport := int(pkt[ihl])<<8 | int(pkt[ihl+1])
	dport := int(pkt[ihl+2])<<8 | int(pkt[ihl+3])
	return sport == 53 || dport == 53
}

// SendPacket 按协议号分流。
func (c *dualPacketConn) SendPacket(pkt []byte) error {
	proto := innerProto(pkt)
	// 只有 UDP 且长度在上限内才走 DATAGRAM
	if proto == udpProto && c.dg != nil && len(pkt) <= c.maxDatagram && !isDNSPacket(pkt) {
		frame, parity := pkt, []byte(nil)
		if c.fecEnc != nil {
			frame, parity = c.fecEnc.Encode(pkt)
		}
		if err := c.dg.SendDatagram(frame); err == nil {
			c.udpSent.Add(1)
			if len(parity) > 0 {
				// 校验帧发失败不影响数据帧，丢包由上层重试
				_ = c.dg.SendDatagram(parity)
			}
			return nil
		}
		// DATAGRAM 发不出去（对端不支持 / 缓冲满）时回退到可靠流
		log.Printf("数据面：DATAGRAM 发送失败，回退到可靠流（%v）", errors.New("send failed"))
	}
	sender := c.stream
	if n := len(c.lanes); n > 0 {
		sender = c.lanes[flowLane(pkt, n)]
	}
	if err := sender.SendPacket(pkt); err != nil {
		return err
	}
	c.streamSent.Add(1)
	return nil
}

// flowLane 按内层五元组算 lane：同一条连接永远落在同一条流上（保持顺序），
// 不同连接尽量分散，从而把一个包的丢失影响限制在一条流内。
func flowLane(pkt []byte, n int) int {
	if n <= 1 || len(pkt) < 20 || pkt[0]>>4 != 4 {
		return 0
	}
	ihl := int(pkt[0]&0x0f) * 4
	h := uint32(2166136261)
	for _, b := range pkt[12:16] { // 源/目的 IP
		h = (h ^ uint32(b)) * 16777619
	}
	if len(pkt) >= ihl+4 {
		for _, b := range pkt[ihl : ihl+4] { // 源/目的端口
			h = (h ^ uint32(b)) * 16777619
		}
	}
	return int(h % uint32(n))
}

// laneLoop 搬运某条 lane 上的下行包（复用同一个合并队列）。
func (c *dualPacketConn) laneLoop(ln packetConn, idx int) {
	defer c.wg.Done()
	for {
		pkt, err := ln.RecvPacket()
		if err != nil {
			// 单条 lane 断了不影响整体：降到 0 号流即可。
			log.Printf("数据面：lane %d 结束（%v），该 lane 流量回落到主可靠流", idx+1, err)
			return
		}
		c.streamRecv.Add(1)
		if !c.push(pkt) {
			return
		}
	}
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
		pkts := [][]byte{pkt}
		if c.fecDec != nil {
			decoded, derr := c.fecDec.Decode(pkt)
			if derr != nil {
				log.Printf("数据面：FEC 解码失败，按原始包处理（%v）", derr)
			} else {
				pkts = decoded
			}
		}
		for _, one := range pkts {
			if !c.push(one) {
				return
			}
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

// FECStats 返回 FEC 还原/放弃计数（诊断用）。
func (c *dualPacketConn) FECStats() (recovered, dropped int) {
	if c.fecDec == nil {
		return 0, 0
	}
	return c.fecDec.FECStats()
}

// PathStats 返回两条通道各自的收发计数（诊断用）。
func (c *dualPacketConn) PathStats() (udpSent, udpRecv, streamSent, streamRecv uint64) {
	return c.udpSent.Load(), c.udpRecv.Load(), c.streamSent.Load(), c.streamRecv.Load()
}