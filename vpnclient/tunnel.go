package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"sync"
	"sync/atomic"

	"time"
)

// Tunnel 是一次已建立的隧道：虚拟网卡 + QUIC 数据面 + 路由接管。
type Tunnel struct {
	dev    PacketDevice
	conn   packetConn
	res    TunResult
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	rxBytes atomic.Int64
	txBytes atomic.Int64
	rxPkts  atomic.Int64
	txPkts  atomic.Int64
	// badInner 统计「帧长度 != IP 头声明长度」的报文数（会被节点判为坏包）
	badInner atomic.Int64
	lastErr  atomic.Value // string
}

// Tunnel 数据面使用 packetConn（可靠流实现见 stream_tunnel.go）。

// TunOptions 是建立隧道所需的参数。
type TunOptions struct {
	Spec    TunSpec
	Conn    packetConn
	OnStats func(rx, tx int64) // 可选：统计回调（每秒）
}

// StartTunnel 创建网卡、配置地址与路由，并启动收发协程。
func StartTunnel(ctx context.Context, opts TunOptions) (*Tunnel, error) {
	if opts.Conn == nil {
		return nil, errors.New("缺少数据面连接")
	}
	spec := opts.Spec
	dev, err := openTUN(spec.name())
	if err != nil {
		return nil, err
	}
	res, err := applyTun(spec, dev)
	if err != nil {
		_ = dev.Close()
		return nil, err
	}

	tctx, cancel := context.WithCancel(ctx)
	t := &Tunnel{dev: dev, conn: opts.Conn, res: res, ctx: tctx, cancel: cancel}
	t.lastErr.Store("")

	t.wg.Add(2)
	go t.pumpUp()
	go t.pumpDown()
	t.wg.Add(1)
	go t.countLog()
	if opts.OnStats != nil {
		t.wg.Add(1)
		go t.reportLoop(opts.OnStats)
	}
	log.Printf("隧道已建立：%s，路由 %v", res.Summary(), res.Routes)
	return t, nil
}

// pumpUp 读网卡 → 发 QUIC DATAGRAM。
func (t *Tunnel) pumpUp() {
	defer t.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("数据面（上行）异常退出：%v", r)
			t.lastErr.Store(fmt.Sprint(r))
			t.cancel()
		}
	}()
	log.Printf("数据面：网卡→隧道 协程已启动")
	first := true
	// 读缓冲必须是 64KB 级别：Windows 的发送卸载（GSO/LSO）会把多个分段
	// 一次性交给网卡驱动，单个超级包可达几十 KB；用 2048 去读会被截断，
	// 我们就发出一堆「IP 头声明 1278 字节、实际只有 40 字节」的残包。
	buf := make([]byte, maxReadPacket)
	for {
		if t.ctx.Err() != nil {
			return
		}
		n, err := t.dev.Read(buf)
		if err != nil {
			if t.ctx.Err() == nil {
				t.fail(fmt.Sprintf("读取网卡失败：%v", err))
			}
			return
		}
		if n <= 0 {
			continue
		}
		if first {
			log.Printf("数据面：从网卡读到首个报文（%d 字节）", n)
			first = false
		}
		raw := buf[:n]

		// 一次读到的东西可能是「一个包」，也可能是 Windows 卸递交上来的
		// 「多段合体包」。按 IP 头里的 total length 逐段切开发送。
		sent, bad, leftover, _ := t.sendRawIPBuffer(raw)
		if sent > 0 {
			t.txPkts.Add(int64(sent))
		}
		if bad > 0 {
			cnt := t.badInner.Add(int64(bad))
			if int(cnt) <= 8 {
				logInnerMismatch(raw, int(cnt))
			}
		}
		if leftover > 0 && t.ctx.Err() == nil {
			// IPv6 背景流量（MLD/NS）也会走到这里，按次数限流打印
			if n := t.badInner.Load(); n <= 8 {
				log.Printf("数据面：缓冲尾部 %d 字节凑不成完整 IP 报文，已丢弃", leftover)
			}
		}
	}
}

// sendRawIPBuffer 把一次网卡读取的字节按 IP 报文切开逐个送出。
//
// 返回：成功发出的包数、判为异常的包数、凑不成完整报文而丢弃的字节数，
// 以及「长度不够、可以等下次读补齐」的残包。
func (t *Tunnel) sendRawIPBuffer(raw []byte) (sent, bad, leftover int, partial []byte) {
	off := 0
	for off < len(raw) {
		rest := raw[off:]
		pkt := stripIPv4(rest)
		if len(pkt) == 0 {
			return sent, bad, len(rest), nil
		}
		skip := len(rest) - len(pkt) // stripIPv4 可能跳过的前导字节
		total := ipTotalLen(pkt)
		if total < 20 {
			return sent, bad + 1, len(pkt), nil
		}
		if total > len(pkt) {
			// 长度不足：交回残包供调用方记录，但**不做跨读取拼接**
			// （试过拼接，会把下一个新包一起当成续传，导致 100% 错位）
			buf := make([]byte, len(pkt))
			copy(buf, pkt)
			return sent, bad + 1, 0, buf
		}
		pkt = pkt[:total]
		if err := t.conn.SendPacket(pkt); err != nil {
			if t.ctx.Err() == nil {
				t.fail(fmt.Sprintf("发送失败：%v", err))
			}
			return sent, bad, 0, nil
		}
		sent++
		t.txBytes.Add(int64(len(pkt)))
		off += skip + total
	}
	return sent, bad, 0, nil
}

// pumpDown 收 QUIC DATAGRAM → 写网卡。
func (t *Tunnel) pumpDown() {
	defer t.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			log.Printf("数据面（下行）异常退出：%v", r)
			t.lastErr.Store(fmt.Sprint(r))
			t.cancel()
		}
	}()
	log.Printf("数据面：隧道→网卡 协程已启动")
	for {
		if t.ctx.Err() != nil {
			return
		}
		data, err := t.conn.RecvPacket()
		if err != nil {
			if t.ctx.Err() == nil && !errors.Is(err, context.Canceled) {
				t.fail(fmt.Sprintf("接收失败：%v", err))
			}
			return
		}
		pkt, _, _, perr := strip(data)
		if perr != nil || len(pkt) == 0 {
			continue
		}
		if _, err := t.dev.Write(pkt); err != nil {
			if t.ctx.Err() == nil {
				t.fail(fmt.Sprintf("写入网卡失败：%v", err))
			}
			return
		}
		t.rxBytes.Add(int64(len(pkt)))
		t.rxPkts.Add(1)
	}
}

func (t *Tunnel) reportLoop(fn func(rx, tx int64)) {
	defer t.wg.Done()
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-tk.C:
			fn(t.rxBytes.Load(), t.txBytes.Load())
		}
	}
}

func (t *Tunnel) fail(msg string) {
	t.lastErr.Store(msg)
	log.Printf("隧道中断：%s", msg)
	t.cancel()
}

// Stats 返回累计收发字节。
func (t *Tunnel) Stats() (rx, tx int64) { return t.rxBytes.Load(), t.txBytes.Load() }

// Result 返回接管结果（网卡名 / 路由）。
func (t *Tunnel) Result() TunResult { return t.res }

// Err 返回最近一次中断原因。
func (t *Tunnel) Err() string {
	if v, ok := t.lastErr.Load().(string); ok {
		return v
	}
	return ""
}

// Close 停止收发、清理路由并关闭网卡。
func (t *Tunnel) Close() {
	t.cancel()
	done := make(chan struct{})
	go func() { t.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	cleanupTun(t.res)
	_ = t.dev.Close()
	log.Printf("隧道已关闭：%s", t.res.Summary())
}

// stripIPv4 归一化从网卡读到的包：去掉可能存在的 4 字节 AF 头。
func stripIPv4(b []byte) []byte {
	if len(b) >= 20 && b[0]>>4 == 4 {
		return b
	}
	if len(b) > 4 && b[4]>>4 == 4 {
		return b[4:]
	}
	return nil
}

// execCommand / hideWindow 由平台文件提供（Windows 需要隐藏黑窗）。
var execCommand = exec.Command

// runtimeSupportsTun 当前平台是否支持创建网卡与路由。
func runtimeSupportsTun() bool {
	switch runtime.GOOS {
	case "linux", "windows", "darwin":
		return true
	}
	return false
}

// countLog 每 5 秒打印一次网卡收发包数，便于判断流量是否真的进入隧道。
func (t *Tunnel) countLog() {
	defer t.wg.Done()
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	var lastRx, lastTx int64
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-tk.C:
			rx, tx := t.rxPkts.Load(), t.txPkts.Load()
			if rx != lastRx || tx != lastTx {
				log.Printf("隧道流量：网卡→隧道 %d 包（+%d），隧道→网卡 %d 包（+%d）",
					tx, tx-lastTx, rx, rx-lastRx)
				lastRx, lastTx = rx, tx
			} else {
				log.Printf("隧道流量：仍为 0（网卡→隧道 %d，隧道→网卡 %d）", tx, rx)
			}
		}
	}
}

// BadInnerPackets 返回「帧长度与 IP 头声明不一致」而被丢弃的报文数。
func (t *Tunnel) BadInnerPackets() int64 { return t.badInner.Load() }

// PacketStats 返回包数统计。
func (t *Tunnel) PacketStats() (rxPkts, txPkts int64) {
	return t.rxPkts.Load(), t.txPkts.Load()
}
