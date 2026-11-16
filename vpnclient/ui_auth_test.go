package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// 假面板：/api/portal/vpn/nodes 返回指定状态码；其它路径 404。
func newAuthServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/portal/vpn/nodes" && r.URL.Path != "/api/portal/vpn/connect" &&
			r.URL.Path != "/api/portal/vpn/devices" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newAuthTestUI 构造只用于认证测试的 uiServer；配置写入临时文件，不碰真实用户目录。
func newAuthTestUI(t *testing.T, api, token string) *uiServer {
	t.Helper()
	prev := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "ppvpn-config.json")
	t.Cleanup(func() { configPathOverride = prev })
	s := &uiServer{
		cfg:   uiSettings{API: api, Token: token, DNSAddr: defaultDNSAddr, MeshCIDR: "10.99.0.0/16"},
		state: &sessionState{status: "idle", message: "未连接"},
		logs:  &logRing{},
	}
	s.setAuthState(authChecking, "")
	if err := saveSettings(s.currentSettings()); err != nil {
		t.Fatalf("准备配置文件失败：%v", err)
	}
	return s
}

func (s *uiServer) testToken() string {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.cfg.Token
}

func (s *uiServer) testAuth() (string, string) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	return s.authState, s.authError
}

func TestValidateAuthValidKeepsToken(t *testing.T) {
	srv := newAuthServer(t, http.StatusOK, `{"nodes":[{"id":1,"name":"n1"}]}`)
	s := newAuthTestUI(t, srv.URL, "tok-valid")
	s.validateAuth(context.Background())
	st, msg := s.testAuth()
	if st != authValid {
		t.Fatalf("auth_state=%q（%s），期望 valid", st, msg)
	}
	if msg != "" {
		t.Fatalf("valid 时 auth_error 应为空，得到 %q", msg)
	}
	if got := s.testToken(); got != "tok-valid" {
		t.Fatalf("valid 不应清令牌，得到 %q", got)
	}
}

func TestValidateAuthExpiredClearsToken(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := newAuthServer(t, code, `{"error":"token expired"}`)
			s := newAuthTestUI(t, srv.URL, "tok-expired")
			s.validateAuth(context.Background())
			st, msg := s.testAuth()
			if st != authInvalid {
				t.Fatalf("HTTP %d 后 auth_state=%q，期望 invalid", code, st)
			}
			if msg != authHintExpired {
				t.Fatalf("auth_error=%q，期望 %q", msg, authHintExpired)
			}
			if got := s.testToken(); got != "" {
				t.Fatalf("HTTP %d 后内存令牌应清空，得到 %q", code, got)
			}
			loaded := loadSettings(uiSettings{})
			if loaded.Token != "" {
				t.Fatalf("HTTP %d 后落盘令牌应清空，得到 %q", code, loaded.Token)
			}
		})
	}
}

func TestValidateAuthNetworkErrorKeepsToken(t *testing.T) {
	// 用不可达地址 + 短父 context 模拟网络错误/超时（panelDo 会因 ctx 结束提前返回）。
	s := newAuthTestUI(t, "http://127.0.0.1:1", "tok-offline")
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	s.validateAuth(ctx)
	st, msg := s.testAuth()
	if st != authUnknown {
		t.Fatalf("网络错误后 auth_state=%q（%s），期望 unknown", st, msg)
	}
	if msg != authHintUnverified {
		t.Fatalf("auth_error=%q，期望 %q", msg, authHintUnverified)
	}
	if got := s.testToken(); got != "tok-offline" {
		t.Fatalf("网络错误不得清除令牌，得到 %q", got)
	}
}

func TestValidateAuthServerErrorKeepsToken(t *testing.T) {
	srv := newAuthServer(t, http.StatusInternalServerError, `{"error":"boom"}`)
	s := newAuthTestUI(t, srv.URL, "tok-5xx")
	s.validateAuth(context.Background())
	st, msg := s.testAuth()
	if st != authUnknown {
		t.Fatalf("5xx 后 auth_state=%q（%s），期望 unknown", st, msg)
	}
	if got := s.testToken(); got != "tok-5xx" {
		t.Fatalf("5xx 不得清除令牌，得到 %q", got)
	}
}

func TestValidateAuthNoTokenInvalid(t *testing.T) {
	s := newAuthTestUI(t, "http://example.invalid", "")
	s.validateAuth(context.Background())
	st, msg := s.testAuth()
	if st != authInvalid || msg != authHintNoToken {
		t.Fatalf("无令牌时 auth=(%q,%q)，期望 (%q,%q)", st, msg, authInvalid, authHintNoToken)
	}
}

func TestHandleStateAuthFields(t *testing.T) {
	cases := []struct {
		name       string
		authState  string
		authError  string
		token      string
		wantLogged bool
		wantValid  bool
		wantInval  bool
	}{
		{"valid", authValid, "", "tok", true, true, false},
		{"invalid", authInvalid, authHintExpired, "", false, false, true},
		{"unknown", authUnknown, authHintUnverified, "tok", false, false, false},
		{"checking", authChecking, "", "tok", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newAuthTestUI(t, "https://panel.example.com", c.token)
			s.setAuthState(c.authState, c.authError)
			rec := httptest.NewRecorder()
			s.handleState(rec, httptest.NewRequest(http.MethodGet, "/api/state", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("HTTP %d", rec.Code)
			}
			var out map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("解析 /api/state 失败：%v", err)
			}
			if got := out["auth_state"]; got != c.authState {
				t.Fatalf("auth_state=%v，期望 %q", got, c.authState)
			}
			if got := out["auth_error"]; got != c.authError {
				t.Fatalf("auth_error=%v，期望 %q", got, c.authError)
			}
			if got := out["logged_in"]; got != c.wantLogged {
				t.Fatalf("logged_in=%v，期望 %v", got, c.wantLogged)
			}
			if got := out["auth_valid"]; got != c.wantValid {
				t.Fatalf("auth_valid=%v，期望 %v", got, c.wantValid)
			}
			if got := out["auth_invalid"]; got != c.wantInval {
				t.Fatalf("auth_invalid=%v，期望 %v", got, c.wantInval)
			}
		})
	}
}

func TestHandleNodesAuthExpiredClearsToken(t *testing.T) {
	srv := newAuthServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`)
	s := newAuthTestUI(t, srv.URL, "tok-nodes")
	rec := httptest.NewRecorder()
	s.handleNodes(rec, httptest.NewRequest(http.MethodGet, "/api/nodes", nil))
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析 /api/nodes 失败：%v", err)
	}
	if got := out["auth_state"]; got != authInvalid {
		t.Fatalf("/api/nodes 的 auth_state=%v，期望 invalid", got)
	}
	if got := s.testToken(); got != "" {
		t.Fatalf("运行中 401 应清除内存令牌，得到 %q", got)
	}
	loaded := loadSettings(uiSettings{})
	if loaded.Token != "" {
		t.Fatalf("运行中 401 应清除落盘令牌，得到 %q", loaded.Token)
	}
}

func TestHandleNodesSuccessMarksValid(t *testing.T) {
	srv := newAuthServer(t, http.StatusOK, `{"nodes":[]}`)
	s := newAuthTestUI(t, srv.URL, "tok-ok")
	s.setAuthState(authUnknown, authHintUnverified)
	rec := httptest.NewRecorder()
	s.handleNodes(rec, httptest.NewRequest(http.MethodGet, "/api/nodes", nil))
	if st, msg := s.testAuth(); st != authValid {
		t.Fatalf("成功拉取节点后 auth_state=%q（%s），期望 valid", st, msg)
	}
}

func TestPanelJSONAuthExpiredTyped(t *testing.T) {
	srv := newAuthServer(t, http.StatusForbidden, `{"error":"forbidden"}`)
	s := newAuthTestUI(t, srv.URL, "tok-json")
	_, err := s.panelJSON(context.Background(), http.MethodGet, "/api/portal/vpn/devices", nil, "tok-json")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("panelJSON 403 应返回 ErrSessionExpired，得到 %v", err)
	}
	// 无令牌的登录类请求不应被当作“会话过期”。
	_, err = s.panelJSON(context.Background(), http.MethodGet, "/api/portal/vpn/devices", nil, "")
	if errors.Is(err, ErrSessionExpired) {
		t.Fatal("无令牌请求不应返回 ErrSessionExpired")
	}
}

func TestListNodesForbiddenIsSessionExpired(t *testing.T) {
	srv := newAuthServer(t, http.StatusForbidden, `{"error":"forbidden"}`)
	_, err := listNodes(context.Background(), srv.URL, "tok")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("listNodes 403 应返回 ErrSessionExpired，得到 %v", err)
	}
}

func TestConnectPortalForbiddenTyped(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/portal/vpn/connect" {
			hits.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"expired"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	_, err := connectPortal(context.Background(), srv.URL, "tok", 1, 2)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("connectPortal 401 应返回 ErrSessionExpired，得到 %v", err)
	}
	if hits.Load() == 0 {
		t.Fatal("假面板未被调用")
	}
}
