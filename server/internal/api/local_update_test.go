package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newLocalUpdateTestHandlers() *handlers {
	return &handlers{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func resetLocalUpdateState() {
	localUpdateState.Lock()
	localUpdateState.running = map[string]bool{}
	localUpdateState.last = map[string]localUpdateResult{}
	localUpdateState.Unlock()
}

func TestLocalUpdateServiceAllowlist(t *testing.T) {
	if name, ok := localUpdateService(" CloudFlared "); !ok || name != "cloudflared" {
		t.Fatalf("cloudflared 应被允许，得到 %q ok=%v", name, ok)
	}
	for _, bad := range []string{"", "nginx", "cloudflared;rm -rf /", "cloudflared/../x"} {
		if _, ok := localUpdateService(bad); ok {
			t.Fatalf("非法服务名 %q 不应通过白名单", bad)
		}
	}
}

func TestBuildLocalUpdateResult(t *testing.T) {
	updated := "BIN=/usr/bin/cloudflared\nOLD=cloudflared version 2026.10.7 (built x)\nNEW=cloudflared version 2026.11.0 (built y)\nACTIVE=active\n"
	r := buildLocalUpdateResult("cloudflared", true, updated, 1, 2)
	if !r.OK || r.Old == "" || r.New == "" || r.Active != "active" {
		t.Fatalf("更新结果解析失败：%+v", r)
	}
	if !strings.Contains(r.Message, "已更新") || !strings.Contains(r.Message, "2026.10.7") {
		t.Fatalf("更新消息不符合预期：%q", r.Message)
	}

	already := "BIN=/usr/bin/cloudflared\nOLD=cloudflared version 2026.11.0\nNEW=cloudflared version 2026.11.0\nACTIVE=active\nALREADY=cloudflared version 2026.11.0\n"
	r = buildLocalUpdateResult("cloudflared", true, already, 1, 2)
	if !strings.Contains(r.Message, "已是最新版本") {
		t.Fatalf("已是最新版本时消息不符合预期：%q", r.Message)
	}

	failed := "curl: (22) 404\n更新失败"
	r = buildLocalUpdateResult("cloudflared", false, failed, 1, 2)
	if r.OK || !strings.Contains(r.Message, "更新失败") {
		t.Fatalf("失败结果不符合预期：%+v", r)
	}
}

func TestLocalUpdateLifecycle(t *testing.T) {
	defer resetLocalUpdateState()
	resetLocalUpdateState()

	origRunner := runLocalUpdateScript
	defer func() { runLocalUpdateScript = origRunner }()

	release := make(chan struct{})
	var once sync.Once
	runLocalUpdateScript = func(ctx context.Context, service string) (bool, string) {
		once.Do(func() { <-release })
		return true, "BIN=/usr/bin/cloudflared\nOLD=cloudflared version 2026.10.7\nNEW=cloudflared version 2026.11.0\nACTIVE=active\n"
	}

	h := newLocalUpdateTestHandlers()

	// 启动更新
	req := httptest.NewRequest(http.MethodPost, "/api/local/services/cloudflared/update", nil)
	req.SetPathValue("name", "cloudflared")
	rec := httptest.NewRecorder()
	h.localServiceUpdateStart(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("启动失败：code=%d body=%s", rec.Code, rec.Body.String())
	}
	var started map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &started)
	if started["started"] != true || started["running"] != true {
		t.Fatalf("启动响应不符合预期：%v", started)
	}

	// 重复启动应返回 started=false（不并发执行）
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/local/services/cloudflared/update", nil)
	req2.SetPathValue("name", "cloudflared")
	h.localServiceUpdateStart(rec2, req2)
	var again map[string]any
	_ = json.Unmarshal(rec2.Body.Bytes(), &again)
	if again["started"] != false || again["running"] != true {
		t.Fatalf("重复启动应提示进行中：%v", again)
	}

	// 查询运行中
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/local/services/cloudflared/update", nil)
	req3.SetPathValue("name", "cloudflared")
	h.localServiceUpdateStatus(rec3, req3)
	var st map[string]any
	_ = json.Unmarshal(rec3.Body.Bytes(), &st)
	if st["running"] != true {
		t.Fatalf("运行状态查询不符合预期：%v", st)
	}

	// 放行，等待完成
	close(release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		localUpdateState.Lock()
		running := localUpdateState.running["cloudflared"]
		last := localUpdateState.last["cloudflared"]
		localUpdateState.Unlock()
		if !running {
			if !last.OK || !strings.Contains(last.Message, "已更新") {
				t.Fatalf("完成结果不符合预期：%+v", last)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待更新完成超时")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 未知服务名必须 400
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodPost, "/api/local/services/nginx/update", nil)
	req4.SetPathValue("name", "nginx")
	h.localServiceUpdateStart(rec4, req4)
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("未知服务应返回 400，得到 %d", rec4.Code)
	}
	errRec := httptest.NewRecorder()
	errReq := httptest.NewRequest(http.MethodGet, "/api/local/services/nginx/update", nil)
	errReq.SetPathValue("name", "nginx")
	h.localServiceUpdateStatus(errRec, errReq)
	if errRec.Code != http.StatusBadRequest {
		t.Fatalf("未知服务状态应返回 400，得到 %d", errRec.Code)
	}
}
