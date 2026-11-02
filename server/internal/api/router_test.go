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

func newHandler() http.Handler {
	cfg := config.Config{JWTSecret: "test-secret", SuperAdmin: "admin"}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return api.New(cfg, nil, log) // 这些用例不触达数据库
}

func TestUnmigratedAPIReturns501(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/singbox/warp", nil)
	newHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("状态码 = %d，期望 501", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "尚未迁移") {
		t.Fatalf("响应体缺少提示: %s", rr.Body.String())
	}
}

func TestProtectedRouteRejectsMissingToken(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	newHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rr.Code)
	}
}

func TestProtectedRouteRejectsBadToken(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
	req.Header.Set("Authorization", "Bearer not-a-jwt")
	newHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", rr.Code)
	}
}

func TestCORSHeadersAndPreflight(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/login", nil)
	newHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("预检状态码 = %d，期望 204", rr.Code)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("CORS = %q", got)
	}
}

func TestUnknownPageReturns404(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nope", nil)
	newHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，期望 404", rr.Code)
	}
}
