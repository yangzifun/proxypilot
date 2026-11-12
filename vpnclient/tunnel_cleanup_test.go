package main

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDev 是 PacketDevice 的测试替身（不触碰真实网卡）。
type fakeDev struct {
	mu     sync.Mutex
	closes int
}

func (f *fakeDev) Name() string                { return "faketun" }
func (f *fakeDev) Read(p []byte) (int, error)  { return 0, nil }
func (f *fakeDev) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeDev) Close() error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	return nil
}

func (f *fakeDev) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

// countCleanup 把生产用的 cleanupTun 换成计数替身。
// 计数即代表「路由 / DNS 守卫 / 断线保护 / IPv6 已被还原」这一步真的被执行了。
func countCleanup(t *testing.T) *atomic.Int32 {
	t.Helper()
	prev := cleanupTunFn
	var n atomic.Int32
	cleanupTunFn = func(TunResult) { n.Add(1) }
	t.Cleanup(func() { cleanupTunFn = prev })
	return &n
}

// newTestTunnel 造一个不启动收发协程的 Tunnel。
func newTestTunnel(dev PacketDevice) *Tunnel {
	ctx, cancel := context.WithCancel(context.Background())
	tun := &Tunnel{
		dev: dev, ctx: ctx, cancel: cancel,
		res: TunResult{Adapter: "faketun", IP: "10.99.0.2", Gateway: "10.99.0.1", Routes: []string{"0.0.0.0/1", "128.0.0.0/1"}},
	}
	tun.lastErr.Store("")
	return tun
}

// P0 回归：Tunnel.Close 必须在所有断开路径上都会被调用，且幂等
// （重复调用不报错、不重复清理）。
func TestTunnelCloseIsIdempotent(t *testing.T) {
	cases := []struct {
		name       string
		closeTimes int
	}{
		{name: "关闭一次", closeTimes: 1},
		{name: "重复关闭三次", closeTimes: 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cleanup := countCleanup(t)
			dev := &fakeDev{}
			tun := newTestTunnel(dev)
			for i := 0; i < c.closeTimes; i++ {
				tun.Close()
			}
			if got := cleanup.Load(); got != 1 {
				t.Fatalf("cleanupTun 应恰好调用 1 次（幂等），实际 %d", got)
			}
			if got := dev.closeCount(); got != 1 {
				t.Fatalf("网卡应恰好关闭 1 次，实际 %d", got)
			}
		})
	}
}

// P0 回归：隧道运行期自断 / 用户断开都必须把已接管的网络还原。
//
// 修复前：保活循环发现 tun.Err() 后只置 error 状态并 cancel，直接 return，
// 不调 tun.Close() → cleanupTun 不执行 → 0.0.0.0/1、128.0.0.0/1 路由、
// DNS 守卫、断线保护、物理网卡 IPv6 全部残留，整机 IPv4 被黑洞。
func TestRunConnectedSessionRestoresNetworkOnEveryExit(t *testing.T) {
	cases := []struct {
		name string
		// tunErr 非空表示数据面自断
		tunErr string
		// userCancel=true 模拟用户先点「断开」（disconnect 会 cancel ctx 并清理）
		userCancel bool
		wantStatus string
		wantMsg    string
	}{
		{
			name: "隧道运行期自断", tunErr: "session replaced",
			wantStatus: "idle", wantMsg: "隧道中断，已还原网络",
		},
		{
			name: "用户断开同时会话协程退出", userCancel: true,
			wantStatus: "idle", wantMsg: "用户断开",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cleanup := countCleanup(t)
			dev := &fakeDev{}
			tun := newTestTunnel(dev)
			if c.tunErr != "" {
				tun.lastErr.Store(c.tunErr)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			st := &sessionState{status: "connected", message: "已连接", tun: tun, cancel: cancel}
			s := &uiServer{state: st}

			if c.userCancel {
				go func() {
					time.Sleep(5 * time.Millisecond)
					s.disconnect("用户断开")
				}()
			}

			done := make(chan struct{})
			go func() {
				s.runConnectedSession(ctx, 0, nil, tun, 2*time.Millisecond)
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("会话协程没有退出（自断路径可能又变成 return 不清理）")
			}

			if got := cleanup.Load(); got != 1 {
				t.Fatalf("断开后 cleanupTun 应恰好执行 1 次（路由/DNS/断线保护已还原），实际 %d", got)
			}
			if got := dev.closeCount(); got != 1 {
				t.Fatalf("网卡应恰好关闭 1 次，实际 %d", got)
			}
			st.mu.Lock()
			status, msg, tunInState := st.status, st.message, st.tun
			st.mu.Unlock()
			if status != c.wantStatus {
				t.Fatalf("状态=%q，期望 %q（msg=%q）", status, c.wantStatus, msg)
			}
			if !strings.Contains(msg, c.wantMsg) {
				t.Fatalf("状态消息=%q，应包含 %q", msg, c.wantMsg)
			}
			if tunInState != nil {
				t.Fatal("断开后 state.tun 必须置空，否则会留下「error 但资源在」的中间态")
			}
		})
	}
}

// 数据面没建起来（StartTunnel 失败）时不能乱清理：没有 tun 就没有接管。
func TestRunConnectedSessionSkipsCleanupWithoutTunnel(t *testing.T) {
	cleanup := countCleanup(t)
	s := &uiServer{state: &sessionState{status: "connecting"}}
	s.runConnectedSession(context.Background(), 0, nil, nil, time.Millisecond)
	if got := cleanup.Load(); got != 0 {
		t.Fatalf("没有隧道时不应调用 cleanupTun，实际 %d", got)
	}
}
