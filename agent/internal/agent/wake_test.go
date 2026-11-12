package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/config"
	"proxypilot-agent/internal/metrics"
	"proxypilot-agent/internal/runner"
	"proxypilot-agent/internal/state"
)

// P1-3 回归：唤醒必须绕过 interval-5 的大节流窗口，只保留 wakeMinGap 最小冷却。
//
// 修复前：任何在「距离上次 poll 不足 interval-5」时到达的 wake 都会被 Once()
// 整轮跳过（return nil），Daemon 以为本轮成功，再等一个完整轮询周期
// （生产 next_poll_sec 通常 60s），任务下发延迟退化为一个轮询周期。
func TestOnceWakeBypassesIntervalThrottle(t *testing.T) {
	const now = int64(1700000000)
	cases := []struct {
		name        string
		force       bool
		lastPollAgo int64 // <0 表示从没 poll 过
		wantPoll    bool
	}{
		{name: "普通轮询：只差 10s 不满间隔则应跳过", force: false, lastPollAgo: 10, wantPoll: false},
		{name: "唤醒：间隔没到也要真正 poll", force: true, lastPollAgo: 10, wantPoll: true},
		{name: "唤醒：落在最小冷却内仍防抖", force: true, lastPollAgo: 2, wantPoll: false},
		{name: "唤醒：没有上次记录直接 poll", force: true, lastPollAgo: -1, wantPoll: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fw := &fakeWorker{}
			calls := []string{}
			a, st := newTestAgent(t, fw, &calls)
			if err := st.SetNextPoll(60); err != nil {
				t.Fatal(err)
			}
			if c.lastPollAgo >= 0 {
				if err := st.SetLastPoll(now - c.lastPollAgo); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.once(context.Background(), c.force); err != nil {
				t.Fatalf("once 失败：%v", err)
			}
			got := fw.polls.Load() > 0
			if got != c.wantPoll {
				t.Fatalf("poll 次数=%d，期望 poll=%v（唤醒被节流吞掉？）", fw.polls.Load(), c.wantPoll)
			}
		})
	}
}

// wakeDelay 必须保证「等待结束后距离上次 poll ≥ wakeMinGap」，
// 否则唤醒会在 once(ctx, true) 里被最小冷却再次跳过。
func TestWakeDelayCoversMinCooldown(t *testing.T) {
	prev := wakeMinGap
	wakeMinGap = 5 * time.Second
	defer func() { wakeMinGap = prev }()

	cases := []struct {
		name    string
		elapsed time.Duration
		wantMin time.Duration
		wantMax time.Duration
	}{
		{name: "刚 poll 完被唤醒", elapsed: 0, wantMin: wakeMinGap, wantMax: wakeMinGap + 300*time.Millisecond},
		{name: "冷却差 2 秒", elapsed: 3 * time.Second, wantMin: 2 * time.Second, wantMax: 2*time.Second + 300*time.Millisecond},
		{name: "早已超过冷却", elapsed: 30 * time.Second, wantMin: 0, wantMax: 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wakeDelay(c.elapsed)
			if got < c.wantMin || got > c.wantMax {
				t.Fatalf("wakeDelay(%s)=%s，期望 [%s, %s]", c.elapsed, got, c.wantMin, c.wantMax)
			}
			if c.elapsed+got < wakeMinGap {
				t.Fatalf("等待后仍不足最小冷却：elapsed=%s delay=%s", c.elapsed, got)
			}
		})
	}
}

// wsFakeWorker 同时提供 poll 与 WebSocket：Daemon 走「WS 已连接」分支，
// 只有 wake 能触发第二次 poll（否则要等 interval=60s）。
type wsFakeWorker struct {
	polls atomic.Int32
	srv   *httptest.Server
}

func (f *wsFakeWorker) start(t *testing.T) {
	t.Helper()
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/poll":
			f.polls.Add(1)
			_ = json.NewEncoder(w).Encode(client.PollResponse{OK: true, ServerID: 4, NextPollSec: 60})
		case "/api/agent/ws":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close(websocket.StatusNormalClosure, "")
			<-r.Context().Done() // 保持连接，直到测试结束
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(f.srv.Close)
}

// 端到端：WS 连着时来一次 wake，必须在远小于 interval 的时间内再 poll 一次；
// 同时不能在冷却外乱 poll（证明防抖仍在）。
func TestDaemonWakeTriggersPollWithoutWaitingInterval(t *testing.T) {
	prev := wakeMinGap
	wakeMinGap = 50 * time.Millisecond // 注入短冷却，避免测试等 5s
	defer func() { wakeMinGap = prev }()

	fw := &wsFakeWorker{}
	fw.start(t)
	dir := t.TempDir()
	st := state.Store{Dir: filepath.Join(dir, "state")}
	cfg := config.Config{
		Origin: fw.srv.URL, Key: "ag_" + strings.Repeat("a", 64), Interval: 60,
		StateDir: st.Dir, Services: []string{"xray"},
	}
	a := New(Options{
		Config: cfg,
		Client: client.New(fw.srv.URL, cfg.Key, Version, false),
		Store:  st,
		Runner: &runner.Runner{Store: st, Log: func(string, ...any) {}, Shell: func(context.Context, string, int) (string, int) { return "", 0 }},
		Collect: func() metrics.Metrics {
			return metrics.Metrics{Hostname: "hk-01", Platform: "linux/amd64"}
		},
		Log: func(string, ...any) {},
	})
	if err := st.SetNextPoll(60); err != nil { // 模拟生产的 60s 轮询间隔
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Daemon(ctx) }()

	// 第一次 poll 由启动触发；WS 连上后 signalWake → 第二次 poll。
	polls := func() int32 { return fw.polls.Load() }
	deadline := time.Now().Add(3 * time.Second)
	for polls() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if polls() < 2 {
		cancel()
		t.Fatalf("WS 唤醒没有触发第二次 poll（polls=%d）：唤醒被节流吞掉", polls())
	}
	// 第二次 poll 之后必须回到正常轮询（60s），不能连着刷
	before := polls()
	time.Sleep(800 * time.Millisecond)
	if got := polls(); got != before {
		t.Fatalf("唤醒后不应连续 poll（防抖失效）：%d -> %d", before, got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Daemon 应正常退出：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Daemon 未响应取消")
	}
}
