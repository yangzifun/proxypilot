package api

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testJSONLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestClientErrorReportPersistsBestEffort(t *testing.T) {
	resetClientErrorLimit(t)
	fdb, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	body, _ := json.Marshal(map[string]any{
		"message": strings.Repeat("m", 600),
		"stack":   strings.Repeat("s", 5000),
		"url":     "https://example.com/page?token=SECRET",
		"page":    "/page",
		"mode":    "portal",
		"ua":      strings.Repeat("u", 400),
	})
	rec, req := clientErrorTestRequest(string(body))
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
		t.Fatalf("上报应 200：%d %s", rec.Code, rec.Body.String())
	}
	call, ok := fdb.lastExec("INSERT INTO client_errors")
	if !ok || len(call.Args) != 7 {
		t.Fatalf("应 best-effort 写入 client_errors：%#v", call)
	}
	if got := []rune(call.Args[0].(string)); len(got) != 500 {
		t.Fatalf("落库 message 截断=%d", len(got))
	}
	if got := []rune(call.Args[1].(string)); len(got) != 4000 {
		t.Fatalf("落库 stack 截断=%d", len(got))
	}
	if got := call.Args[2].(string); strings.Contains(got, "SECRET") || strings.Contains(got, "?") {
		t.Fatalf("落库 URL 未去敏：%q", got)
	}
	if call.Args[4] != "portal" {
		t.Fatalf("mode=%v", call.Args[4])
	}
	if call.Args[6].(int64) == 0 {
		t.Fatal("created_at 应写入")
	}
}

func TestClientErrorReportDBFailureStillSuccess(t *testing.T) {
	resetClientErrorLimit(t)
	fdb, db := newFakeDB(t)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO client_errors") {
			return nil, errors.New("db down")
		}
		return fakeResult(1, 0), nil
	}
	var buf bytes.Buffer
	h := &handlers{db: db, log: testJSONLogger(&buf)}
	rec, req := clientErrorTestRequest(`{"message":"boom"}`)
	h.clientErrorReport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DB 失败仍应 success 200：%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(buf.String(), "客户端错误落库失败") {
		t.Fatalf("缺少落库失败日志：%s", buf.String())
	}
}

func TestClientErrorsListPaginationAndFilter(t *testing.T) {
	fdb, db := newFakeDB(t)
	var selectQuery string
	var selectArgs []any
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COUNT(*) FROM client_errors") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(2)}), nil
		}
		if strings.Contains(q, "FROM client_errors") {
			selectQuery = q
			selectArgs = fakeArgsAny(args)
			return fakeRowsSet(
				[]string{"id", "message", "stack", "url", "page", "mode", "ua", "created_at"},
				[]driver.Value{int64(2), "err2", "stack2", "https://x/y", "/p2", "admin", "ua2", int64(2000)},
				[]driver.Value{int64(1), "err1", "stack1", "https://x/y", "/p1", "admin", "ua1", int64(1000)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodGet, "/api/client-errors?page=2&size=50&mode=admin", nil)
	req = adminReqWithClaims(req, adminTestClaims("admin"))
	rec := httptest.NewRecorder()
	h.clientErrorsList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Success bool                `json:"success"`
		Total   int64               `json:"total"`
		Page    int                 `json:"page"`
		Size    int                 `json:"size"`
		Data    []clientErrorRecord `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Success || out.Total != 2 || out.Page != 2 || out.Size != 50 || len(out.Data) != 2 {
		t.Fatalf("列表响应异常：%+v", out)
	}
	if out.Data[0].ID != 2 || out.Data[0].Mode != "admin" {
		t.Fatalf("首行异常：%+v", out.Data[0])
	}
	if !strings.Contains(selectQuery, "WHERE mode = ?") || len(selectArgs) != 3 ||
		selectArgs[0] != "admin" || selectArgs[1] != int64(50) || selectArgs[2] != int64(50) {
		t.Fatalf("分页/过滤 SQL 参数异常：q=%s args=%#v", selectQuery, selectArgs)
	}

	// 默认 page=1/size=20；size 最大 100。
	selectArgs = nil
	req = httptest.NewRequest(http.MethodGet, "/api/client-errors?page=0&size=1000", nil)
	req = adminReqWithClaims(req, adminTestClaims("admin"))
	rec = httptest.NewRecorder()
	h.clientErrorsList(rec, req)
	if rec.Code != http.StatusOK || len(selectArgs) != 2 || selectArgs[0] != int64(100) || selectArgs[1] != int64(0) {
		t.Fatalf("默认/上限分页异常：%d args=%#v", rec.Code, selectArgs)
	}
}

func TestCleanupClientErrorsRetention(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.settings = map[string]string{
		"client_error_retention_days": "30",
		"client_error_cleanup_at":     "0",
	}
	h.settingsAt = time.Now()
	now := int64(1800000000000)
	h.cleanupClientErrors(context.Background(), now)

	call, ok := fdb.lastExec("DELETE FROM client_errors")
	if !ok {
		t.Fatal("未执行 client_errors 清理")
	}
	wantCutoff := now - 30*24*3600*1000
	if len(call.Args) != 1 || call.Args[0] != wantCutoff {
		t.Fatalf("清理 cutoff 异常：%#v，期望 %d", call.Args, wantCutoff)
	}
	var gotCleanupAt bool
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "INSERT INTO app_settings") && len(c.Args) >= 1 && c.Args[0] == "client_error_cleanup_at" {
			gotCleanupAt = true
		}
	}
	if !gotCleanupAt {
		t.Fatalf("未记录清理时间")
	}

	// 低于 7 天按 7 天处理。
	h2 := &handlers{db: db, log: testLogger()}
	h2.settings = map[string]string{"client_error_retention_days": "1"}
	h2.settingsAt = time.Now()
	h2.cleanupClientErrors(context.Background(), now)
	call, _ = fdb.lastExec("DELETE FROM client_errors")
	if call.Args[0] != now-7*24*3600*1000 {
		t.Fatalf("保留期下限 7 天未生效：%#v", call.Args)
	}
}
