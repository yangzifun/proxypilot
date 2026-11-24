package api

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetClientErrorLimit(t *testing.T) {
	t.Helper()
	clientErrorRate = newClientErrorLimiter()
	old := clientErrorRateLimit
	t.Cleanup(func() {
		clientErrorRate = newClientErrorLimiter()
		clientErrorRateLimit = old
	})
}

func clientErrorTestHandler(w io.Writer) *handlers {
	return &handlers{log: slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))}
}

func clientErrorTestRequest(body string) (*httptest.ResponseRecorder, *http.Request) {
	req := httptest.NewRequest(http.MethodPost, "/api/client-errors", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7:54321"
	rec := httptest.NewRecorder()
	return rec, req
}

func lastClientErrorLog(t *testing.T, buf []byte) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(string(buf)), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		t.Fatal("未产生客户端错误日志")
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &entry); err != nil {
		t.Fatalf("解析日志失败：%v\n%s", err, lines[len(lines)-1])
	}
	return entry
}

func TestClientErrorReportSuccessAndTruncate(t *testing.T) {
	resetClientErrorLimit(t)
	var buf bytes.Buffer
	h := clientErrorTestHandler(&buf)
	body, _ := json.Marshal(map[string]any{
		"message": strings.Repeat("m", 600),
		"stack":   strings.Repeat("s", 5000),
		"url":     "https://example.com/app?token=SECRET-TOKEN",
		"page":    strings.Repeat("p", 600),
		"mode":    "admin",
		"ua":      strings.Repeat("u", 400),
		"at":      1790000000000,
	})
	rec, req := clientErrorTestRequest(string(body))
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("合法上报应 200 success：%d %s", rec.Code, rec.Body.String())
	}
	entry := lastClientErrorLog(t, buf.Bytes())
	if entry["mode"] != "admin" {
		t.Fatalf("mode = %v", entry["mode"])
	}
	if got := []rune(entry["message"].(string)); len(got) != 500 {
		t.Fatalf("message 截断长度 = %d，期望 500", len(got))
	}
	if got := []rune(entry["stack"].(string)); len(got) != 4000 {
		t.Fatalf("stack 截断长度 = %d，期望 4000", len(got))
	}
	if got := []rune(entry["page"].(string)); len(got) != 500 {
		t.Fatalf("page 截断长度 = %d，期望 500", len(got))
	}
	if got := []rune(entry["ua"].(string)); len(got) != 300 {
		t.Fatalf("ua 截断长度 = %d，期望 300", len(got))
	}
	gotURL, _ := entry["url"].(string)
	if len([]rune(gotURL)) > 500 || strings.Contains(gotURL, "SECRET-TOKEN") || strings.Contains(gotURL, "?") {
		t.Fatalf("url 应截断且去除敏感 query：%q", gotURL)
	}
	if entry["at"] != "1790000000000" {
		t.Fatalf("at = %v", entry["at"])
	}
}

func TestClientErrorReportInvalidJSON(t *testing.T) {
	resetClientErrorLimit(t)
	h := clientErrorTestHandler(io.Discard)
	rec, req := clientErrorTestRequest(`{"message":`)
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 JSON 应 400，实际 %d", rec.Code)
	}
}

func TestClientErrorReportTooLarge(t *testing.T) {
	resetClientErrorLimit(t)
	h := clientErrorTestHandler(io.Discard)
	body := `{"message":"` + strings.Repeat("x", clientErrorMaxBody) + `"}`
	rec, req := clientErrorTestRequest(body)
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("超过 16KB 应 413，实际 %d", rec.Code)
	}
}

func TestClientErrorReportRateLimit(t *testing.T) {
	resetClientErrorLimit(t)
	clientErrorRateLimit = 2
	h := clientErrorTestHandler(io.Discard)
	for i := 0; i < 2; i++ {
		rec, req := clientErrorTestRequest(`{"message":"boom"}`)
		h.clientErrorReport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次上报应 200，实际 %d", i+1, rec.Code)
		}
	}
	rec, req := clientErrorTestRequest(`{"message":"boom"}`)
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超过限速应 429，实际 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 应带 Retry-After")
	}
}

func TestClientErrorReportMethodAndEmptyJSON(t *testing.T) {
	resetClientErrorLimit(t)
	h := clientErrorTestHandler(io.Discard)
	rec := httptest.NewRecorder()
	h.clientErrorReport(rec, httptest.NewRequest(http.MethodGet, "/api/client-errors", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("GET 应 405 Allow POST：%d %q", rec.Code, rec.Header().Get("Allow"))
	}
	rec, req := clientErrorTestRequest("")
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空 body 应 400，实际 %d", rec.Code)
	}
}

func TestClientErrorRateLimiterWindow(t *testing.T) {
	l := newClientErrorLimiter()
	now := time.Unix(1770000000, 0)
	if !l.allow("1.2.3.4", 2, now) || !l.allow("1.2.3.4", 2, now) {
		t.Fatal("限速窗口前两次应通过")
	}
	if l.allow("1.2.3.4", 2, now) {
		t.Fatal("第三次应被限速")
	}
	if !l.allow("1.2.3.4", 2, now.Add(time.Minute)) {
		t.Fatal("下一分钟应恢复")
	}
}
