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

func handlerWith(cfg config.Config) http.Handler {
	cfg.JWTSecret = "test-secret"
	cfg.SuperAdmin = "admin"
	return api.New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestProxyForwardsToLegacyWithAuth(t *testing.T) {
	var gotAuth, gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQuery = r.URL.RawQuery
		w.Header().Set("X-Upstream", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	h := handlerWith(config.Config{LegacyBase: upstream.URL})
	req := httptest.NewRequest(http.MethodGet, "/api/singbox/warp?x=1", nil)
	req.Header.Set("Authorization", "Bearer abc.def.ghi")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 201", rr.Code)
	}
	if gotAuth != "Bearer abc.def.ghi" {
		t.Fatalf("Authorization 未透传: %q", gotAuth)
	}
	if gotQuery != "x=1" {
		t.Fatalf("查询串未透传: %q", gotQuery)
	}
	if rr.Header().Get("X-Upstream") != "1" {
		t.Fatal("上游响应头未回传")
	}
	if rr.Body.String() != `{"ok":true}` {
		t.Fatalf("响应体 = %q", rr.Body.String())
	}
}

func TestProxyNotUsedForPages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("页面路径不应转发")
	}))
	defer upstream.Close()

	h := handlerWith(config.Config{LegacyBase: upstream.URL})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（内嵌前端）", rr.Code)
	}
}

func TestAPIKeyRejectedOnRouteOutsideWhitelist(t *testing.T) {
	h := handlerWith(config.Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("X-API-Key", "pk_test")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "API 密钥不可访问") {
		t.Fatalf("响应 = %s", rr.Body.String())
	}
}

func TestLoginDisabledWhenOIDCOnly(t *testing.T) {
	h := handlerWith(config.Config{OIDCDisablePassword: true})
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
