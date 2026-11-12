package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// blockingDatagram 故意**不监听 ctx**：只靠 CloseWithError 解除阻塞。
//
// 修复前的 dualPacketConn.datagramLoop 调的是
// ReceiveDatagram(context.Background())，done 关闭传不进去；
// Close() 又先 wg.Wait() 再 CloseWithError —— 于是 Wait 永不返回。
type blockingDatagram struct {
	mu         sync.Mutex
	closeCalls int
	closed     chan struct{}
	once       sync.Once
}

func newBlockingDatagram() *blockingDatagram {
	return &blockingDatagram{closed: make(chan struct{})}
}

func (b *blockingDatagram) SendDatagram([]byte) error { return nil }

func (b *blockingDatagram) ReceiveDatagram(context.Context) ([]byte, error) {
	<-b.closed
	return nil, errors.New("datagram 已关闭")
}

func (b *blockingDatagram) CloseWithError(uint64, string) error {
	b.once.Do(func() {
		b.mu.Lock()
		b.closeCalls++
		b.mu.Unlock()
		close(b.closed)
	})
	return nil
}

func (b *blockingDatagram) Done() <-chan struct{} { return b.closed }

func (b *blockingDatagram) calls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.closeCalls
}

// isClosed 供本测试断言可靠流是否已被关闭状态。
func (f *fakeStream) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// P1-5 回归：Close() 必须先关闭/取消底层连接再 Wait，否则断开时死锁，
// 客户端 QUIC 连接、UDP socket、协程永不释放。
func TestDualConnCloseClosesTransportBeforeWait(t *testing.T) {
	cases := []struct {
		name       string
		withDG     bool
		withLane   bool
		wantDGCall int
	}{
		{name: "DATAGRAM 阻塞在接收上", withDG: true, wantDGCall: 1},
		{name: "没有 DATAGRAM 通道", withDG: false, wantDGCall: 0},
		{name: "DATAGRAM + 附加 lane", withDG: true, withLane: true, wantDGCall: 1},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newFakeStream()
			var dg *blockingDatagram
			var dgi datagramPeer
			if c.withDG {
				dg = newBlockingDatagram()
				dgi = dg
			}
			var lanes []packetConn
			var lane *fakeStream
			if c.withLane {
				lane = newFakeStream()
				lanes = append(lanes, lane)
			}
			conn := newDualPacketConnLanes(st, lanes, dgi, 1400, 0)

			done := make(chan error, 1)
			go func() { done <- conn.Close() }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Close 不应报错：%v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Close 死锁：阻塞在 ReceiveDatagram 的协程没有被先关闭")
			}

			if c.wantDGCall > 0 && dg.calls() != c.wantDGCall {
				t.Fatalf("CloseWithError 调用次数=%d，期望 %d", dg.calls(), c.wantDGCall)
			}
			if !st.isClosed() {
				t.Fatal("可靠流未关闭")
			}
			if lane != nil && !lane.isClosed() {
				t.Fatal("lane 未关闭")
			}

			// 幂等：再关一次不能卡住，也不能重复清理
			done2 := make(chan error, 1)
			go func() { done2 <- conn.Close() }()
			select {
			case <-done2:
			case <-time.After(2 * time.Second):
				t.Fatal("第二次 Close 卡住（必须幂等）")
			}
			if dg != nil && dg.calls() != 1 {
				t.Fatalf("重复 Close 不应重复关闭底层连接：%d", dg.calls())
			}
		})
	}
}
