package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"proxypilot-agent/internal/client"
	"proxypilot-agent/internal/config"
	"proxypilot-agent/internal/metrics"
	"proxypilot-agent/internal/runner"
	"proxypilot-agent/internal/state"
)

// fakeWorker 模拟 Worker 的 poll / ack 端点。
type fakeWorker struct {
	polls    atomic.Int32
	acks     atomic.Int32
	lastPoll client.PollRequest
	lastAck  client.AckRequest
	wire     string
	status   int
}

func (f *fakeWorker) start(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/poll":
			f.polls.Add(1)
			var body client.PollRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastPoll = body
			if f.status != 0 {
				w.WriteHeader(f.status)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(client.PollResponse{
				OK: true, ServerID: 4, ServerName: "hk-01", NextPollSec: 120, Wire: f.wire,
			})
		case "/api/agent/ack":
			f.acks.Add(1)
			var body client.AckRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.lastAck = body
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func execLine(id int, command string, timeout int) string {
	return strings.Join([]string{
		strconv.Itoa(id), "exec", "0", "", "", "", "", "0", "", "", "", "0", b64(command), strconv.Itoa(timeout),
	}, "|")
}

func newTestAgent(t *testing.T, fw *fakeWorker, calls *[]string) (*Agent, state.Store) {
	t.Helper()
	srv := fw.start(t)
	dir := t.TempDir()
	st := state.Store{Dir: filepath.Join(dir, "state")}
	cfg := config.Config{
		Origin: srv.URL, Key: "ag_" + strings.Repeat("a", 64), Interval: 60,
		StateDir: st.Dir, Services: []string{"xray"},
	}
	a := New(Options{
		Config: cfg,
		Client: client.New(srv.URL, cfg.Key, Version, false),
		Store:  st,
		Runner: &runner.Runner{
			Store: st,
			Log:   func(string, ...any) {},
			Shell: func(ctx context.Context, script string, timeoutSec int) (string, int) {
				*calls = append(*calls, script)
				return "ok", 0
			},
		},
		Collect: func() metrics.Metrics {
			return metrics.Metrics{Hostname: "hk-01", Load: "1,2,3", Cores: 2, Platform: "linux/amd64", Services: map[string]string{"xray": "active"}}
		},
		Now: func() time.Time { return time.Unix(1700000000, 0) },
		Log: func(string, ...any) {},
	})
	return a, st
}

func TestOnceExecutesWireTasksAndAcks(t *testing.T) {
	fw := &fakeWorker{wire: execLine(9, "echo hi", 15)}
	calls := []string{}
	a, st := newTestAgent(t, fw, &calls)
	if err := a.Once(context.Background()); err != nil {
		t.Fatalf("Once 失败：%v", err)
	}
	if len(calls) != 1 || calls[0] != "echo hi" {
		t.Fatalf("任务未执行：%v", calls)
	}
	if fw.acks.Load() != 1 {
		t.Fatalf("应回执 1 次：%d", fw.acks.Load())
	}
	if len(fw.lastAck.Results) != 1 || fw.lastAck.Results[0].ID != 9 || fw.lastAck.Results[0].Status != "ok" {
		t.Fatalf("回执内容错：%+v", fw.lastAck)
	}
	if fw.lastAck.AgentVersion != Version {
		t.Fatalf("回执应带版本：%+v", fw.lastAck)
	}
	if got := st.NextPoll(); got != 120 {
		t.Fatalf("服务端间隔未落盘：%d", got)
	}
	if fw.lastPoll.Hostname != "hk-01" || fw.lastPoll.Load != "1,2,3" || fw.lastPoll.AgentVersion != Version {
		t.Fatalf("poll 请求体错：%+v", fw.lastPoll)
	}
	if fw.lastPoll.Platform != "linux/amd64" {
		t.Fatalf("platform 应随 poll 上报：%q", fw.lastPoll.Platform)
	}
	if st.LastPoll() == 0 {
		t.Fatal("last_poll 未写入")
	}
}

func TestOnceNoTasksSkipsAck(t *testing.T) {
	fw := &fakeWorker{}
	calls := []string{}
	a, _ := newTestAgent(t, fw, &calls)
	if err := a.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fw.acks.Load() != 0 {
		t.Fatalf("无任务不应 ack：%d", fw.acks.Load())
	}
}

func TestOnceThrottleSkipsRound(t *testing.T) {
	fw := &fakeWorker{}
	calls := []string{}
	a, st := newTestAgent(t, fw, &calls)
	if err := st.SetNextPoll(60); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLastPoll(1700000000 - 10); err != nil {
		t.Fatal(err)
	}
	if err := a.Once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if fw.polls.Load() != 0 {
		t.Fatalf("节流期内不应 poll：%d", fw.polls.Load())
	}
}

func TestOncePollFailureReturnsError(t *testing.T) {
	fw := &fakeWorker{status: 500}
	calls := []string{}
	a, _ := newTestAgent(t, fw, &calls)
	if err := a.Once(context.Background()); err == nil {
		t.Fatal("poll 失败应返回错误（供退避）")
	}
	if fw.acks.Load() != 0 {
		t.Fatal("poll 失败不应 ack")
	}
}

func TestDaemonStopsOnContextCancel(t *testing.T) {
	fw := &fakeWorker{}
	calls := []string{}
	a, st := newTestAgent(t, fw, &calls)
	if err := st.SetNextPoll(10); err != nil { // 最短间隔，避免长等
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Daemon(ctx) }()
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Daemon 应正常退出：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Daemon 未响应取消")
	}
	if fw.polls.Load() < 1 {
		t.Fatalf("应至少 poll 一次：%d", fw.polls.Load())
	}
}

func TestClampInterval(t *testing.T) {
	if clampInterval(0, 60) != 60 || clampInterval(1, 60) != config.MinInterval || clampInterval(99999, 60) != config.MaxInterval {
		t.Fatalf("间隔钳制错：%d %d %d", clampInterval(0, 60), clampInterval(1, 60), clampInterval(99999, 60))
	}
}
