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

	"github.com/quic-go/quic-go"
	"time"
)

// Tunnel 是一次已建立的隧道：虚拟网卡 + QUIC 数据面 + 路由接管。
type Tunnel struct {
	dev    PacketDevice
	conn   datagramConn
	res    TunResult
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	rxBytes atomic.Int64
	txBytes atomic.Int64
	lastErr atomic.Value // string
}

// datagramConn 抽象 quic.Conn 里用到的方法，便于测试替换。
type datagramConn interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
	CloseWithError(code quic.ApplicationErrorCode, msg string) error
}

// TunOptions 是建立隧道所需的参数。
type TunOptions struct {
	Spec    TunSpec
	Conn    datagramConn
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
	buf := make([]byte, 2048)
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
		pkt := stripIPv4(buf[:n])
		if len(pkt) == 0 {
			continue
		}
		if err := t.conn.SendDatagram(pkt); err != nil {
			if t.ctx.Err() == nil {
				t.fail(fmt.Sprintf("发送失败：%v", err))
			}
			return
		}
		t.txBytes.Add(int64(len(pkt)))
	}
}

// pumpDown 收 QUIC DATAGRAM → 写网卡。
func (t *Tunnel) pumpDown() {
	defer t.wg.Done()
	for {
		if t.ctx.Err() != nil {
			return
		}
		data, err := t.conn.ReceiveDatagram(t.ctx)
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