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

// adminRoleDB 模拟 admin_users 的 role 查询：
//   - admin: is_root=1,is_super=1
//   - extra: is_root=0,is_super=1
//   - normal: is_root=0,is_super=0
//
// rootRows 固定为 1，表示 DB 已有 root 行（旧 cfg 用户名不再兜底）。
func adminRoleDB(t *testing.T) (*fakeDB, *handlers) {
	t.Helper()
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		user := ""
		if len(args) > 0 {
			user, _ = args[0].(string)
		}
		if strings.Contains(q, "SELECT COALESCE((SELECT is_root") {
			switch user {
			case "admin":
				return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(1), int64(1)}), nil
			case "newroot":
				return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(1), int64(1)}), nil
			default:
				return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(0), int64(1)}), nil
			}
		}
		if strings.Contains(q, "SELECT COALESCE(is_super,0) FROM admin_users") {
			flag := int64(0)
			if user == "admin" || user == "extra" {
				flag = 1
			}
			return fakeRowsSet([]string{"is_super"}, []driver.Value{flag}), nil
		}
		return &fakeRows{}, nil
	}
	return fdb, &handlers{cfg: config.Config{JWTSecret: "admin-super-test-secret", SuperAdmin: "admin"}, db: db, log: testLogger()}
}

func TestIsRootAndSuperAdmin(t *testing.T) {
	if (&handlers{cfg: config.Config{SuperAdmin: "admin"}}).isRootAdmin(context.Background(), "admin") != true {
		t.Fatal("无 DB 时 cfg root 应兼容为 root")
	}
	fdb, h := adminRoleDB(t)
	_ = fdb
	if !h.isRootAdmin(context.Background(), "admin") {
		t.Fatal("DB is_root=1 应是 root")
	}
	if h.isRootAdmin(context.Background(), "extra") {
		t.Fatal("额外超管不是 root")
	}
	if !h.isSuperAdmin(context.Background(), "extra") {
		t.Fatal("额外超管应是 super")
	}
	if h.isSuperAdmin(context.Background(), "normal") {
		t.Fatal("普通管理员不是 super")
	}
	// DB 已有 root 行时，旧 cfg 用户名不再是 root（改名后的核心语义）。
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE((SELECT is_root") {
			return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(0), int64(1)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	if h.isRootAdmin(context.Background(), "admin") {
		t.Fatal("DB 已有 root 行时，旧 cfg 用户名不应再兜底为 root")
	}
}

func TestRootFallbackOnlyWhenNoRootRows(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE((SELECT is_root") {
			return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(0), int64(0)}), nil
		}
		return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(0)}), nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
	if !h.isRootAdmin(context.Background(), "admin") {
		t.Fatal("DB 无 root 行时应回退 PP_SUPER_ADMIN")
	}
	if h.isRootAdmin(context.Background(), "other") {
		t.Fatal("无 root 行时只有 cfg root 回退")
	}
}
func TestSuperOnlyAllowsDatabaseSuperAdmin(t *testing.T) {
	_, h := adminRoleDB(t)
	token := func(sub string) string {
		tok, err := auth.Sign(adminTestClaims(sub), h.cfg.JWTSecret)
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
	fdb, h := adminRoleDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT id, username") && strings.Contains(q, "FROM admin_users") {
			return fakeRowsSet(
				[]string{"id", "username", "is_super", "is_root", "email", "email_verified", "email_verified_at", "mfa_enabled", "last_mfa_login", "created_at"},
				[]driver.Value{int64(1), "admin", int64(1), int64(1), nil, int64(0), int64(0), int64(0), int64(0), int64(0)},
				[]driver.Value{int64(2), "extra", int64(1), int64(0), nil, int64(0), int64(0), int64(0), int64(0), int64(0)},
			), nil
		}
		if strings.Contains(q, "SELECT COALESCE((SELECT is_root") {
			return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(0), int64(1)}), nil
		}
		if strings.Contains(q, "SELECT COALESCE(is_super,0)") {
			return fakeRowsSet([]string{"is_super"}, []driver.Value{int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
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
	if !strings.Contains(rec.Body.String(), `"username":"admin","is_super":true,"is_root":true`) {
		t.Fatalf("root 行角色字段异常：%s", rec.Body.String())
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
		user := ""
		if len(args) > 0 {
			user, _ = args[0].(string)
		}
		if strings.Contains(q, "SELECT email") && strings.Contains(q, "FROM admin_users WHERE username") {
			root, super := int64(0), int64(0)
			if user == "admin" {
				root, super = 1, 1
			}
			if user == "extra" {
				super = 1
			}
			return fakeRowsSet([]string{"email", "email_verified", "is_super", "is_root"},
				[]driver.Value{nil, int64(0), super, root}), nil
		}
		if strings.Contains(q, "SELECT COUNT(*) FROM admin_users WHERE username") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
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

	if code, body := call(`{"username":"admin","is_super":false}`, adminTestClaims("admin")); code != http.StatusBadRequest || !strings.Contains(body, "root") {
		t.Fatalf("root 降级应 400：%d %s", code, body)
	}
	if code, body := call(`{"username":"extra","is_super":false}`, adminTestClaims("extra")); code != http.StatusBadRequest || !strings.Contains(body, "自己") {
		t.Fatalf("自降应 400：%d %s", code, body)
	}
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

func TestAdminUsersRenameRoot(t *testing.T) {
	h, fdb := adminUserUpdateHandler(t)
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPut, "/api/admin/users",
		strings.NewReader(`{"username":"admin","newUsername":"newroot"}`)), adminTestClaims("admin"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.adminUsersUpdate(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("root 改名失败：%d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["username"] != "newroot" || out["relogin_required"] != true || out["is_root"] != true || out["is_super"] != true {
		t.Fatalf("root 改名响应异常：%#v", out)
	}
	// root 行更新必须保留 is_root/is_super；关联表同步更新。
	var rootUpdate, oidcUpdate, emailUpdate, mfaUpdate bool
	for _, c := range fdb.execCalls() {
		switch {
		case strings.Contains(c.Query, "UPDATE admin_users SET username") && strings.Contains(c.Query, "is_root = 1"):
			rootUpdate = true
		case strings.Contains(c.Query, "UPDATE admin_oidc_identities SET username"):
			oidcUpdate = true
		case strings.Contains(c.Query, "UPDATE admin_email_codes SET username"):
			emailUpdate = true
		case strings.Contains(c.Query, "UPDATE mfa_backup_codes SET username"):
			mfaUpdate = true
		}
	}
	if !rootUpdate || !oidcUpdate || !emailUpdate || !mfaUpdate {
		t.Fatalf("root 改名未同步全部关联表：root=%v oidc=%v email=%v mfa=%v", rootUpdate, oidcUpdate, emailUpdate, mfaUpdate)
	}
}

func TestAdminUsersRenameInvalid(t *testing.T) {
	h, _ := adminUserUpdateHandler(t)
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPut, "/api/admin/users",
		strings.NewReader(`{"username":"normal","newUsername":"bad name"}`)), adminTestClaims("admin"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.adminUsersUpdate(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法新用户名应 400：%d %s", rec.Code, rec.Body.String())
	}
}
func TestAdminUsersRenameRejectsDuplicate(t *testing.T) {
	h, fdb := adminUserUpdateHandler(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT email") && strings.Contains(q, "FROM admin_users WHERE username") {
			return fakeRowsSet([]string{"email", "email_verified", "is_super", "is_root"},
				[]driver.Value{nil, int64(0), int64(0), int64(0)}), nil
		}
		if strings.Contains(q, "SELECT COUNT(*) FROM admin_users WHERE username = ? AND username <> ?") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPut, "/api/admin/users",
		strings.NewReader(`{"username":"normal","newUsername":"taken"}`)), adminTestClaims("admin"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.adminUsersUpdate(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("重复用户名应 409：%d %s", rec.Code, rec.Body.String())
	}
}

func TestAdminUsersDeleteRoot(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE((SELECT is_root") {
			return fakeRowsSet([]string{"is_root", "root_rows"}, []driver.Value{int64(1), int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}, db: db, log: testLogger()}
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
	_, h := adminRoleDB(t)
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
	_, h := adminRoleDB(t)
	req := adminReqWithClaims(httptest.NewRequest(http.MethodPost, "/x", nil), adminTestClaims("extra"))
	if !h.adminEmailSelfOrSuper(req, "someone") {
		t.Fatal("DB 超管应可修改他人邮箱")
	}
	normal := adminReqWithClaims(httptest.NewRequest(http.MethodPost, "/x", nil), adminTestClaims("normal"))
	if h.adminEmailSelfOrSuper(normal, "someone") {
		t.Fatal("普通管理员不应可修改他人邮箱")
	}
}

func TestEnsureRootAdmin(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "COUNT(*) FROM admin_users WHERE is_root = 1") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		return &fakeRows{}, nil
	}
	var update string
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE admin_users SET is_root = 1") {
			update = q
		}
		return fakeResult(1, 1), nil
	}
	EnsureRootAdmin(context.Background(), db, "admin", testLogger())
	if update == "" {
		t.Fatal("无 root 行时启动应补 root 标记")
	}
}
func TestLoginAfterRootRename(t *testing.T) {
	fdb, db := newFakeDB(t)
	hash, err := auth.HashPassword("secret1")
	if err != nil {
		t.Fatal(err)
	}
	current := "admin"
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE admin_users SET username") && len(args) > 0 {
			current, _ = args[0].(string)
		}
		return fakeResult(1, 0), nil
	}
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM admin_users WHERE username = ?") && len(args) == 1 && args[0] == current {
			return fakeRowsSet([]string{"id", "username", "password_hash", "mfa_enabled", "mfa_secret"},
				[]driver.Value{int64(1), current, hash, int64(0), nil}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{
		cfg: config.Config{JWTSecret: "rename-login-secret-1234567890", SuperAdmin: "admin", AdminTokenTTL: time.Hour},
		db:  db, log: testLogger(),
		settings: map[string]string{"auth_password_enabled": "1"}, settingsAt: time.Now(),
	}
	if err := h.renameAdminUser(context.Background(), "admin", "newroot", true); err != nil {
		t.Fatalf("rename: %v", err)
	}
	login := func(username string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/login",
			strings.NewReader(`{"username":"`+username+`","password":"secret1"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.login(rec, req)
		return rec.Code
	}
	if code := login("newroot"); code != http.StatusOK {
		t.Fatalf("改名后按新用户名登录应成功，实际 %d", code)
	}
	if code := login("admin"); code != http.StatusUnauthorized {
		t.Fatalf("旧用户名不应再能登录，实际 %d", code)
	}
}
