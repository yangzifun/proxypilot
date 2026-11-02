package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestOIDCStatusDisabled(t *testing.T) {
	h := &handlers{cfg: config.Config{}}
	rec := httptest.NewRecorder()
	h.oidcStatus(rec, httptest.NewRequest(http.MethodGet, "/api/oidc/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != false {
		t.Fatalf("enabled = %v", body["enabled"])
	}

	rec2 := httptest.NewRecorder()
	h.oidcLogin(rec2, httptest.NewRequest(http.MethodGet, "/api/oidc/login", nil))
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("未启用时 login = %d，期望 404", rec2.Code)
	}
}

func TestOIDCEnabledFlag(t *testing.T) {
	h := &handlers{cfg: config.Config{OIDCIssuer: "https://id.example.com", OIDCClientID: "client"}}
	if !h.oidcEnabled() {
		t.Fatal("配置 issuer/client_id 后应启用")
	}
	rec := httptest.NewRecorder()
	h.oidcStatus(rec, httptest.NewRequest(http.MethodGet, "/api/oidc/status", nil))
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["enabled"] != true {
		t.Fatalf("enabled = %v", body["enabled"])
	}
}
