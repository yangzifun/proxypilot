package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/api"
	"github.com/yzfn/proxypilot/server/internal/config"
)

func newCleanupHandler() http.Handler {
	cfg := config.Config{JWTSecret: "test-secret", SuperAdmin: "admin"}
	return api.New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// 旧 Worker 兜底已移除：未注册 API 一律 404，不再 501/代理。
func TestUnknownAPIAndPathReturn404(t *testing.T) {
	t.Setenv("PP_LEGACY_BASE", "http://127.0.0.1:1")
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/singbox/warp"},
		{http.MethodPost, "/api/legacy/anything"},
		{http.MethodGet, "/nope"},
	} {
		rr := httptest.NewRecorder()
		newCleanupHandler().ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d，期望 404", tc.method, tc.path, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "尚未迁移") || strings.Contains(rr.Body.String(), "PP_LEGACY_BASE") {
			t.Fatalf("%s %s 不应再出现旧 Worker 兜底提示：%s", tc.method, tc.path, rr.Body.String())
		}
	}
}

// 审计确认下线的旧路由必须不再注册（保持 404）。
func TestRemovedLegacyRoutes(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/node-templates"},
		{http.MethodPost, "/api/node-templates"},
		{http.MethodGet, "/api/node-templates/1"},
		{http.MethodPut, "/api/node-templates/1"},
		{http.MethodDelete, "/api/node-templates/1"},
		{http.MethodPost, "/api/node-configs/preview"},
		{http.MethodPost, "/api/node-configs/push"},
		{http.MethodGet, "/api/stats/uuid-details"},
		{http.MethodGet, "/api/uuid-configs/stats"},
		{http.MethodGet, "/api/servers/services"},
		{http.MethodGet, "/api/mihomo/stats"},
		{http.MethodGet, "/me"},
		{http.MethodGet, "/ui"},
		{http.MethodGet, "/manage"},
		{http.MethodGet, "/manage/"},
		{http.MethodGet, "/manage/all-uuid-info"},
		{http.MethodGet, "/manage/entry"},
		{http.MethodGet, "/manage/generate-uuid"},
		{http.MethodGet, "/manage/uuid-check/x"},
		{http.MethodGet, "/admin/certs"},
		{http.MethodGet, "/admin/settings"},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		newCleanupHandler().ServeHTTP(rr, httptest.NewRequest(tc.method, tc.path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d，期望 404（路由应已删除）", tc.method, tc.path, rr.Code)
		}
	}
}

func TestLoginDisabledWhenOIDCOnly(t *testing.T) {
	cfg := config.Config{JWTSecret: "test-secret", SuperAdmin: "admin", OIDCDisablePassword: true}
	h := api.New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"admin","password":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "密码登录已禁用") {
		t.Fatalf("响应 = %s", rr.Body.String())
	}
}
