package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// 清理旧页面/旧 API 后，仍被新版前端/门户/Agent 使用的路由必须保持注册。
// 这里用空 fake DB 只验证“没有落到统一 404 兜底”，不依赖具体业务数据。
func TestPreservedRoutesStillRegistered(t *testing.T) {
	_, db := newFakeDB(t)
	h := New(config.Config{JWTSecret: "test-secret", SuperAdmin: "admin"}, db, testLogger())

	cases := []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/admin"},
		{http.MethodGet, "/manage/uuid-info"},
		{http.MethodGet, "/manage/uuid-info/11111111-1111-4111-8111-111111111111"},
		{http.MethodGet, "/manage/configs/11111111-1111-4111-8111-111111111111"},
		{http.MethodGet, "/manage/stats/11111111-1111-4111-8111-111111111111"},
		{http.MethodGet, "/manage/uuid-sub-sources"},
		{http.MethodPost, "/api/node-configs/1/push"},
		{http.MethodGet, "/api/node-configs/logs"},
		{http.MethodPost, "/api/mihomo/report"},
		{http.MethodGet, "/api/mihomo/traffic/summary"},
		{http.MethodGet, "/api/mihomo/bin/amd64"},
	}
	for _, tc := range cases {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(tc.method, tc.path, nil)
		h.ServeHTTP(rr, req)
		if isFallback404(rr.Code, rr.Body.String()) {
			t.Fatalf("%s %s 落到了统一 404（路由不应被删除）", tc.method, tc.path)
		}
	}
}

func isFallback404(code int, body string) bool {
	return code == http.StatusNotFound &&
		strings.Contains(body, `"error":"Not Found"`) &&
		strings.Contains(body, `"path"`)
}

// 删除的旧 Worker 兜底/501 路径必须统一 404，且响应不再包含迁移提示。
func TestLegacyFallbackRemoved(t *testing.T) {
	_, db := newFakeDB(t)
	h := New(config.Config{JWTSecret: "test-secret", SuperAdmin: "admin"}, db, testLogger())
	t.Setenv("PP_LEGACY_BASE", "http://127.0.0.1:1")

	for _, p := range []string{"/api/mail/settings", "/api/xray/status", "/api/unknown/old"} {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s = %d，期望 404", p, rr.Code)
		}
		if strings.Contains(rr.Body.String(), "尚未迁移") || strings.Contains(rr.Body.String(), "PP_LEGACY_BASE") {
			t.Fatalf("%s 仍含旧 Worker 兜底提示：%s", p, rr.Body.String())
		}
	}
}
