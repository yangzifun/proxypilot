package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

func adminTestClaims(sub string) auth.Claims {
	return auth.Claims{Sub: sub, Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}
}

func adminReqWithClaims(r *http.Request, c auth.Claims) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), claimsKey, c))
}

func TestIsSuperAdmin(t *testing.T) {
	root := &handlers{cfg: config.Config{SuperAdmin: "admin"}}
	if !root.isSuperAdmin(context.Background(), "admin") {
		t.Fatal("root 应始终是超管")
	}
	if root.isSuperAdmin(context.Background(), "normal") {
		t.Fatal("无 DB 的普通账号不应是超管")
	}

	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM admin_users WHERE username") && len(args) == 1 && args[0] == "extra" {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
	if !h.isSuperAdmin(context.Background(), "extra") {
		t.Fatal("DB is_super=1 应是超管")
	}
	if h.isSuperAdmin(context.Background(), "normal") {
		t.Fatal("DB is_super=0 不应是超管")
	}
}

func TestSuperOnlyAllowsDatabaseSuperAdmin(t *testing.T) {
	const secret = "admin-super-test-secret"
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM admin_users WHERE username") && len(args) == 1 && args[0] == "extra" {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	h := &handlers{cfg: config.Config{JWTSecret: secret, SuperAdmin: "admin"}, db: db, log: testLogger()}
	token := func(sub string) string {
		tok, err := auth.Sign(adminTestClaims(sub), secret)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	call := func(sub string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+token(sub))
		h.authed(authOpts{superOnly: true}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })(rec, req)
		return rec.Code
	}
	if got := call("extra"); got != http.StatusOK {
		t.Fatalf("DB 额外超管通过 superOnly = %d，期望 200", got)
	}
	if got := call("normal"); got != http.StatusForbidden {
		t.Fatalf("普通管理员通过 superOnly = %d，期望 403", got)
	}
	if got := call("admin"); got != http.StatusOK {
		t.Fatalf("root 通过 superOnly = %d，期望 200", got)
	}
}

func TestAdminUsersListRoleFields(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT id, username") && strings.Contains(q, "FROM admin_users") {
			return fakeRowsSet(
				[]string{"id", "username", "is_super", "email", "email_verified", "email_verified_at", "mfa_enabled", "last_mfa_login", "created_at"},
				[]driver.Value{int64(1), "admin", int64(0), nil, int64(0), int64(0), int64(0), int64(0), int64(0)},
				[]driver.Value{int64(2), "extra", int64(1), nil, int64(0), int64(0), int64(0), int64(0), int64(0)},
			), nil
		}
		if strings.Contains(q, "FROM admin_users WHERE username") && len(args) == 1 && args[0] == "extra" {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
	req := adminReqWithClaims(httptest.NewRequest(http.MethodGet, "/api/admin/users", nil), adminTestClaims("extra"))
	rec := httptest.NewRecorder()
	h.adminUsersList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"is_root":true`) ||
		!strings.Contains(rec.Body.String(), `"username":"extra"`) ||
		!strings.Contains(rec.Body.String(), `"self_is_super":true`) {
		t.Fatalf("列表角色字段缺失：%s", rec.Body.String())
	}
	// root 行即使 DB is_super=0 也必须是 is_super=true。
	if !strings.Contains(rec.Body.String(), `"username":"admin","is_super":true,"is_root":true`) {
		t.Fatalf("root 行应强制 is_super=true：%s", rec.Body.String())
	}
}

func TestAdminUsersCreateIsSuper(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "COUNT(*) FROM admin_users WHERE username") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		if strings.Contains(q, "COUNT(*) FROM admin_users") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	var insertArgs []any
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO admin_users") {
			insertArgs = fakeArgsAny(args)
		}
		return fakeResult(1, 2), nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPost, "/api/admin/users",
		strings.NewReader(`{"username":"extra","password":"secret1","is_super":true}`)), adminTestClaims("admin"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.adminUsersCreate(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"is_super":true`) {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(insertArgs) == 0 || insertArgs[len(insertArgs)-1] != int64(1) {
		t.Fatalf("INSERT 未带 is_super=1：%#v", insertArgs)
	}
}

func adminUserUpdateHandler(t *testing.T) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT email") && strings.Contains(q, "FROM admin_users WHERE username") {
			return fakeRowsSet([]string{"email", "email_verified", "is_super"},
				[]driver.Value{nil, int64(0), int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	return &handlers{cfg: config.Config{JWTSecret: "s", SuperAdmin: "admin"}, db: db, log: testLogger()}, fdb
}

func TestAdminUsersUpdateSuperGuards(t *testing.T) {
	h, fdb := adminUserUpdateHandler(t)
	call := func(body string, claims auth.Claims) (int, string) {
		req := adminReqWithClaims(httptest.NewRequest(http.MethodPut, "/api/admin/users", strings.NewReader(body)), claims)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.adminUsersUpdate(rec, req)
		return rec.Code, rec.Body.String()
	}

	// root 不可降级。
	if code, body := call(`{"username":"admin","is_super":false}`, adminTestClaims("admin")); code != http.StatusBadRequest || !strings.Contains(body, "root") {
		t.Fatalf("root 降级应 400：%d %s", code, body)
	}
	// 当前账号不可自降。
	if code, body := call(`{"username":"extra","is_super":false}`, adminTestClaims("extra")); code != http.StatusBadRequest || !strings.Contains(body, "自己") {
		t.Fatalf("自降应 400：%d %s", code, body)
	}
	// 超管可把普通账号提升为超管。
	if code, body := call(`{"username":"normal","is_super":true}`, adminTestClaims("admin")); code != http.StatusOK {
		t.Fatalf("提升超管失败：%d %s", code, body)
	}
	var found bool
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "UPDATE admin_users SET is_super") && len(c.Args) >= 2 && c.Args[0] == int64(1) {
			found = true
		}
	}
	if !found {
		t.Fatal("未写入 is_super=1")
	}
}

func TestAdminUsersDeleteRoot(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, log: testLogger()}
	req := adminReqWithClaims(httptest.NewRequest(http.MethodDelete, "/api/admin/users",
		strings.NewReader(`{"username":"admin"}`)), adminTestClaims("admin"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.adminUsersDelete(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "root") {
		t.Fatalf("删除 root 应 400：%d %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMe(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM admin_users WHERE username") && len(args) == 1 && args[0] == "extra" {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}

	rec := httptest.NewRecorder()
	req := adminReqWithClaims(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), adminTestClaims("extra"))
	h.authMe(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth/me status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["username"] != "extra" || out["is_super"] != true || out["is_root"] != false {
		t.Fatalf("auth/me 响应异常：%#v", out)
	}

	rec = httptest.NewRecorder()
	req = adminReqWithClaims(httptest.NewRequest(http.MethodGet, "/api/auth/me", nil), adminTestClaims("admin"))
	h.authMe(rec, req)
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["is_root"] != true || out["is_super"] != true {
		t.Fatalf("root auth/me 异常：%#v", out)
	}
}

func TestAdminEmailSelfOrSuperWithDbSuper(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM admin_users WHERE username") && len(args) == 1 && args[0] == "extra" {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPost, "/x", nil), adminTestClaims("extra"))
	if !h.adminEmailSelfOrSuper(req, "someone") {
		t.Fatal("DB 超管应可修改他人邮箱")
	}
	normal := adminReqWithClaims(httptest.NewRequest(http.MethodPost, "/x", nil), adminTestClaims("normal"))
	if h.adminEmailSelfOrSuper(normal, "someone") {
		t.Fatal("普通管理员不应可修改他人邮箱")
	}
}
