package api

import (
	"database/sql/driver"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

func TestClientErrorDeleteOne(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "DELETE FROM client_errors WHERE id = ?") {
			return fakeResult(1, 0), nil
		}
		return fakeResult(0, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodDelete, "/api/client-errors/7", nil)
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.clientErrorDeleteOne(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":1`) {
		t.Fatalf("单条删除失败：%d %s", rec.Code, rec.Body.String())
	}
	call, ok := fdb.lastExec("DELETE FROM client_errors WHERE id = ?")
	if !ok || len(call.Args) != 1 || call.Args[0] != int64(7) {
		t.Fatalf("单条删除参数异常：%#v", call)
	}

	// 不存在返回 404。
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(0, 0), nil }
	rec = httptest.NewRecorder()
	h.clientErrorDeleteOne(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不存在应 404：%d", rec.Code)
	}

	// 非法 ID 400。
	req = httptest.NewRequest(http.MethodDelete, "/api/client-errors/x", nil)
	req.SetPathValue("id", "x")
	rec = httptest.NewRecorder()
	h.clientErrorDeleteOne(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 ID 应 400：%d", rec.Code)
	}
}

func TestClientErrorDeleteBatchFilters(t *testing.T) {
	fdb, db := newFakeDB(t)
	var deleteQuery string
	var deleteArgs []any
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "DELETE FROM client_errors") {
			deleteQuery = q
			deleteArgs = fakeArgsAny(args)
			return fakeResult(3, 0), nil
		}
		return fakeResult(0, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodDelete, "/api/client-errors?mode=admin&before=1800000000000", nil)
	rec := httptest.NewRecorder()
	h.clientErrorDeleteBatch(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"deleted":3`) {
		t.Fatalf("批量删除失败：%d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(deleteQuery, "mode = ?") || !strings.Contains(deleteQuery, "created_at < ?") ||
		len(deleteArgs) != 2 || deleteArgs[0] != "admin" || deleteArgs[1] != int64(1800000000000) {
		t.Fatalf("批量删除条件异常：q=%s args=%#v", deleteQuery, deleteArgs)
	}

	// 空条件拒绝，不能误删全部。
	req = httptest.NewRequest(http.MethodDelete, "/api/client-errors", nil)
	rec = httptest.NewRecorder()
	h.clientErrorDeleteBatch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空条件应 400：%d", rec.Code)
	}

	// 非法 before。
	req = httptest.NewRequest(http.MethodDelete, "/api/client-errors?before=abc", nil)
	rec = httptest.NewRecorder()
	h.clientErrorDeleteBatch(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 before 应 400：%d", rec.Code)
	}
}

func TestClientErrorExportCSV(t *testing.T) {
	fdb, db := newFakeDB(t)
	var exportQuery string
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM client_errors") {
			exportQuery = q
			return fakeRowsSet(
				[]string{"id", "created_at", "mode", "page", "url", "message", "ua"},
				[]driver.Value{int64(2), int64(2000), "admin", "/page", "https://example.com/x", "hello, \"world\"\nnext", "UA/2"},
				[]driver.Value{int64(1), int64(1000), "portal", "/portal", "https://example.com/y", "ok", "UA/1"},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodGet, "/api/client-errors/export", nil)
	rec := httptest.NewRecorder()
	h.clientErrorExport(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("导出失败：%d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="client-errors.csv"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(exportQuery, "ORDER BY created_at DESC") || !strings.Contains(exportQuery, "LIMIT 10000") {
		t.Fatalf("导出 SQL 排序/上限异常：%s", exportQuery)
	}
	records, err := csv.NewReader(strings.NewReader(rec.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("CSV 解析失败：%v\n%s", err, rec.Body.String())
	}
	if len(records) != 3 {
		t.Fatalf("CSV 行数 = %d，期望 3：%s", len(records), rec.Body.String())
	}
	wantHeader := []string{"id", "created_at", "mode", "page", "url", "message", "ua"}
	for i := range wantHeader {
		if records[0][i] != wantHeader[i] {
			t.Fatalf("CSV 表头 = %#v", records[0])
		}
	}
	if records[1][0] != "2" || records[1][5] != "hello, \"world\"\nnext" {
		t.Fatalf("CSV 首行 = %#v", records[1])
	}
}

func TestClientErrorAdminRequiresSuper(t *testing.T) {
	_, h := adminRoleDB(t)
	token, err := auth.Sign(adminTestClaims("normal"), h.cfg.JWTSecret)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/client-errors/1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", "1")
	rec := httptest.NewRecorder()
	h.authed(authOpts{superOnly: true}, h.clientErrorDeleteOne)(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("普通管理员应 403，实际 %d", rec.Code)
	}
}
