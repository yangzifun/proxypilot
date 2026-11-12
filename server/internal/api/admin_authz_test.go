package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// 权限模型：普通管理员可用所有后台功能，唯独「管理账号」（/api/admin/users，
// 前端 admin#sysusers）仍然只有超级管理员可进。
//
// 这个测试锁住两件事，防止以后有人「顺手」把 superOnly 加回去或者反过来：
//   1. authOpts{}  —— 普通管理员通过
//   2. authOpts{superOnly:true} —— 普通管理员 403、超管通过
func TestAdminAuthzSuperOnlyIsUsersOnly(t *testing.T) {
	const secret = "test-secret-for-admin-authz"
	const superAdmin = "admin"
	h := &handlers{cfg: config.Config{JWTSecret: secret, SuperAdmin: superAdmin}}

	ordinary, err := auth.Sign(auth.Claims{Sub: "shao", Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}, secret)
	if err != nil {
		t.Fatalf("签发普通管理员令牌失败：%v", err)
	}
	super, err := auth.Sign(auth.Claims{Sub: superAdmin, Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}, secret)
	if err != nil {
		t.Fatalf("签发超管令牌失败：%v", err)
	}

	call := func(opts authOpts, token string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		h.authed(opts, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})(rec, req)
		return rec.Code
	}

	// 普通后台接口（已放开）：普通管理员也要 200
	if got := call(authOpts{}, ordinary); got != http.StatusOK {
		t.Fatalf("普通管理员访问普通接口 = %d，期望 200", got)
	}
	// 管理账号接口（保留超管）：普通管理员必须 403
	if got := call(authOpts{superOnly: true}, ordinary); got != http.StatusForbidden {
		t.Fatalf("普通管理员访问 sysusers 接口 = %d，期望 403", got)
	}
	// 超管两边都通
	if got := call(authOpts{superOnly: true}, super); got != http.StatusOK {
		t.Fatalf("超管访问 sysusers 接口 = %d，期望 200", got)
	}
}