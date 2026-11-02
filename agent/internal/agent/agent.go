// Package agent 编排一轮同步（采集 → poll → 执行任务 → ack）与常驻循环。
package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/config"
	"proxypilot-agent/internal/metrics"
	"proxypilot-agent/internal/runner"
	"proxypilot-agent/internal/shell"
	"proxypilot-agent/internal/state"
)

// Version Agent 版本（随 poll 上报，后台「服务器管理」列表显示）。
// 1.4.0：新增 mtg-multi 自动安装与 ExecReload 写入（MTProto 部署）
// 1.4.1：采集 sing-box / xray / mtg-multi 版本号并随 poll 上报（服务器详情展示）
// 1.5.0：模板可指定 sing-box / xray 版本，下发时按需安装并切换（正式版 / 预览版）
// 1.5.1：上报 SSH 服务状态（监控页只读展示），配合服务器管理里的批量启停
// 1.5.2：部署时确保 systemd 单元存在；配置未变但服务未运行会自动拉起（修复 MTProto 下发后未启用）
// 1.5.3：SSH 状态同时检查 service 与 socket（Ubuntu 套接字激活），区分「运行中但未自启」
// 1.5.4：长轮询等任务（Durable Object 唤醒），下发延迟从轮询间隔降到秒级；旧 Worker 自动回退轮询
// 1.5.5：长轮询覆盖完整轮询周期（默认 60s），任务/WebSSH 仍秒级唤醒；旧服务端自动回退
// 1.6.0：改用 WebSocket（DO Hibernation）接收 wake，空闲不再占用 DO Duration；连不上时回退长轮询
const Version = "1.7.3"

// maxActionsPerRound 单轮执行任务数上限（服务端 AGENT_ACTIONS_PER_POLL = 5，这里留足余量）。
const maxActionsPerRound = 20

// Options 依赖注入（便于单测）。
type Options struct {
	Config  config.Config
	Client  *client.Client
	Runner  *runner.Runner
	Store   state.Store
	Collect func() metrics.Metrics
	Log     func(format string, args ...any)
	Now     func() time.Time
}

// Agent 是一台机器的接入实例。
type Agent struct {
	opts Options
	mu   sync.Mutex
	// activeShell 当前正在跑的实时终端 session（同一时间只保留一个）
	activeShell string
	// wake WebSocket 收到消息后唤醒主循环立即 poll；wsConnected 为真时不再走长轮询
	wake        chan struct{}
	wsConnected atomic.Bool
}

// New 构造 Agent（补齐默认实现）。
func New(opts Options) *Agent {
	if opts.Store.Dir == "" {
		opts.Store = state.Store{Dir: opts.Config.StateDir}
	}
	if opts.Runner == nil {
		opts.Runner = &runner.Runner{Store: opts.Store, Log: opts.Log, InstallMissing: true}
	}
	if opts.Client == nil {
		opts.Client = client.New(opts.Config.Origin, opts.Config.Key, Version, opts.Config.Insecure)
	}
	if opts.Collect == nil {
		services := opts.Config.Services
		opts.Collect = func() metrics.Metrics { return metrics.Collect(metrics.Options{Services: services}) }
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Agent{opts: opts, wake: make(chan struct{}, 1)}
}

// Once 执行一轮：采集 → poll → 执行 wire 任务 → ack。
// 返回的 error 仅表示本轮失败（调用方决定退避）；无任务不算失败。
func (a *Agent) Once(ctx context.Context) error {
	if err := a.opts.Store.Ensure(); err != nil {
		a.opts.Log("创建状态目录失败：%v", err)
	}
	now := a.opts.Now()
	interval := a.interval()
	if last := a.opts.Store.LastPoll(); last > 0 && now.Unix()-last < int64(interval-5) {
		a.opts.Log("距上次 poll 不足 %ds，本轮跳过", interval-5)
		return nil
	}
	_ = a.opts.Store.SetLastPoll(now.Unix())

	m := a.opts.Collect()
	req := client.PollRequest{
		AgentVersion: Version,
		Hostname:     m.Hostname,
		OS:           m.OS,
		Kernel:       m.Kernel,
		Platform:     m.Platform,
		Arch:         m.Arch,
		Cores:        m.Cores,
		UptimeSec:    m.UptimeSec,
		Load:         m.Load,
		MemUsedMB:    m.MemUsedMB,
		MemTotalMB:   m.MemTotalMB,
		DiskPercent:  m.DiskPercent,
		CPUTicks:     m.CPUTicks,
		Services:     m.Services,
		Versions:     m.Versions,
		SSH:          m.SSH,
		TCPPorts:     m.TCPPorts,
		IP:           m.IP,
		IPv6:         m.IPv6,
		Net:          m.Net,
		DiskIO:       m.DiskIO,
		Conns:        m.Conns,
		Procs:        m.Procs,
		Applied:      a.opts.Store.Applied(),
	}
	resp, err := a.opts.Client.Poll(ctx, req)
	if err != nil {
		a.opts.Log("poll 失败：%v", err)
		return err
	}
	next := clampInterval(resp.NextPollSec, a.opts.Config.Interval)
	if resp.NextPollSec > 0 {
		_ = a.opts.Store.SetNextPoll(next)
	}
	a.opts.Log("poll 成功：服务器=%s 下一轮=%ds 任务=%d", nameOr(resp.ServerName, fmt.Sprintf("#%d", resp.ServerID)), next, len(client.ParseWire(resp.Wire)))

	/* 后台打开了实时终端 → 拨号接入（goroutine 里跑，不阻塞心跳） */
	a.maybeStartShell(ctx, &resp)

	actions := client.ParseWire(resp.Wire)
	results := make([]client.Result, 0, len(actions))
	for i, action := range actions {
		if i >= maxActionsPerRound {
			a.opts.Log("本轮任务数超过 %d，其余留待下一轮", maxActionsPerRound)
			break
		}
		results = append(results, a.opts.Runner.Run(ctx, action))
	}
	if len(results) == 0 {
		return nil
	}
	if err := a.ack(ctx, results); err != nil {
		a.opts.Log("ack 失败：%v（服务端将在 10 分钟后回收任务）", err)
		return err
	}
	a.opts.Log("ack 成功：%d 条结果", len(results))
	return nil
}

// maybeStartShell 收到 shell hint 时起一个实时终端 goroutine（同一时间只允许一个）。
func (a *Agent) maybeStartShell(ctx context.Context, resp *client.PollResponse) {
	if resp == nil || resp.Shell == nil || resp.Shell.Session == "" {
		return
	}
	sid := resp.Shell.Session
	a.mu.Lock()
	if a.activeShell != "" {
		a.mu.Unlock()
		return
	}
	a.activeShell = sid
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.activeShell = ""
			a.mu.Unlock()
		}()
		a.opts.Log("实时终端：接入会话 %s", sid)
		err := shell.Serve(ctx, shell.Options{
			Origin:   a.opts.Config.Origin,
			Key:      a.opts.Config.Key,
			ServerID: resp.ServerID,
			Session:  sid,
			Insecure: a.opts.Config.Insecure,
			Log:      a.opts.Log,
			Version:  Version,
		})
		if err != nil {
			a.opts.Log("实时终端结束：%v", err)
			return
		}
		a.opts.Log("实时终端已结束")
	}()
}

// ack 回执（最多 3 次尝试，退避 2s/5s；服务端对重复 ack 幂等）。
func (a *Agent) ack(ctx context.Context, results []client.Result) error {
	req := client.AckRequest{AgentVersion: Version, Results: results}
	backoff := []time.Duration{0, 2 * time.Second, 5 * time.Second}
	var err error
	for attempt, wait := range backoff {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		if err = a.opts.Client.Ack(ctx, req); err == nil {
			return nil
		}
		if isPermanent(err) {
			return err
		}
		a.opts.Log("ack 第 %d 次失败：%v", attempt+1, err)
	}
	return err
}

// Daemon 常驻循环：成功先 poll，再用长轮询等任务（入队即唤醒）；失败按指数退避（上限 300s）。
func (a *Agent) Daemon(ctx context.Context) error {
	failures := 0
	go a.wsLoop(ctx)
	for {
		lastPoll := time.Now()
		err := a.Once(ctx)
		delay := time.Duration(a.interval()) * time.Second
		if err != nil {
			failures++
			backoff := math.Min(float64(time.Duration(a.interval())*time.Second)*math.Pow(2, float64(failures)), float64(300*time.Second))
			delay = time.Duration(backoff)
			a.opts.Log("本轮失败（连续 %d 次），%s 后重试", failures, delay)
		} else {
			failures = 0
			if a.wsConnected.Load() {
				// WebSocket 已连上：等一个心跳周期，或收到 wake 后立即 poll；
				// 不再发 /api/agent/wait，DO 空闲可休眠
				timer := time.NewTimer(time.Duration(a.interval()) * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil
				case <-a.wake:
					timer.Stop()
					delay = 300 * time.Millisecond
				case <-timer.C:
					delay = 0
				}
			} else {
				// 兼容旧 Worker / WS 连不上：保留长轮询，避免任务和 WebSSH 等一个周期
				due := lastPoll.Add(time.Duration(a.interval()) * time.Second)
				woke, werr := a.waitUntilDue(ctx, due)
				switch {
				case werr == nil && woke:
					// Worker 有 5s 防刷：醒来后补足冷却再 poll，避免 429
					gap := 5*time.Second - time.Since(lastPoll) + 200*time.Millisecond
					if gap > 0 {
						delay = gap
					} else {
						delay = 0
					}
				case werr == nil:
					// 等到了预定的 poll 时间
					delay = 0
				case errors.Is(werr, client.ErrWaitUnsupported):
					// 旧 Worker / 未绑定 AGENT_HUB：保持原轮询间隔
				default:
					a.opts.Log("长轮询失败：%v，按原间隔继续", werr)
				}
			}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil
		}
		select {
		case <-ctx.Done():
			a.opts.Log("收到退出信号，已停止")
			return nil
		case <-time.After(delay):
		}
	}
}

// wsLoop 维持到 Worker/DO 的 WebSocket；断线指数退避重连，连上期间主循环不再长轮询。
func (a *Agent) wsLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := a.wsOnce(ctx)
		a.wsConnected.Store(false)
		a.signalWake()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			a.opts.Log("WebSocket 断开：%v，%s 后重连", err, backoff)
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (a *Agent) signalWake() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func (a *Agent) wsOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(dialCtx, a.opts.Client.WSURL(), &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Agent-Token": []string{a.opts.Client.Key}},
	})
	if err != nil {
		return err
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")

	a.wsConnected.Store(true)
	a.signalWake()

	// 心跳：防止 NAT / 中间设备把空闲连接掐掉；失败则关闭连接交给重连
	pingCtx, pingCancel := context.WithCancel(ctx)
	defer pingCancel()
	go func() {
		ticker := time.NewTicker(45 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
				err := ws.Ping(pctx)
				pcancel()
				if err != nil {
					_ = ws.Close(websocket.StatusGoingAway, "ping failed")
					return
				}
			}
		}
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		if string(data) == "wake" {
			a.signalWake()
		}
	}
}

// waitUntilDue 持续长轮询直到 due（单次最多 25s）。返回 true 表示被任务唤醒。
func (a *Agent) waitUntilDue(ctx context.Context, due time.Time) (bool, error) {
	for {
		remaining := time.Until(due)
		if remaining <= 0 {
			return false, nil
		}
		timeout := int(remaining.Seconds())
		if timeout > 25 {
			timeout = 25
		}
		if timeout < 1 {
			timeout = 1
		}
		waitCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+10)*time.Second)
		woke, err := a.opts.Client.Wait(waitCtx, timeout)
		cancel()
		if err != nil {
			return false, err
		}
		if woke {
			return true, nil
		}
	}
}

// interval 本轮使用的间隔：优先服务端上次下发的 next_poll，其次配置值。
func (a *Agent) interval() int {
	if n := a.opts.Store.NextPoll(); n > 0 {
		return clampInterval(n, a.opts.Config.Interval)
	}
	return clampInterval(a.opts.Config.Interval, 60)
}

// Proactive 主动结束（不做事，仅用于让 caller 判断是否配置可用）。
func (a *Agent) Validate() error { return a.opts.Config.Validate() }

func clampInterval(n, fallback int) int {
	if n <= 0 {
		n = fallback
	}
	if n < config.MinInterval {
		return config.MinInterval
	}
	if n > config.MaxInterval {
		return config.MaxInterval
	}
	return n
}

// isPermanent 鉴权类错误不重试（4xx 除 429 外）。
func isPermanent(err error) bool {
	se := &client.StatusError{}
	if errors.As(err, &se) {
		return se.Status >= 400 && se.Status < 500 && se.Status != 429
	}
	return false
}

func nameOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
