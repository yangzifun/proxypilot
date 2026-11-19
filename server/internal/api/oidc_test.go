package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
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

// newOIDCFlowTestHandlers 起一个假 IdP discovery 端点，并返回已启用 OIDC 的 handlers。
// turnstileSecret 传非空值可证明“即使配置了 Turnstile，SSO 入口也不再校验它”。
func newOIDCFlowTestHandlers(t *testing.T, turnstileSecret string) (*handlers, *httptest.Server) {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
			"jwks_uri":               srv.URL + "/jwks",
		})
	}))
	t.Cleanup(srv.Close)
	h := &handlers{cfg: config.Config{
		OIDCIssuer:      srv.URL,
		OIDCClientID:    "test-client",
		TurnstileSecret: turnstileSecret,
		PublicBaseURL:   "https://panel.example.com",
	}}
	return h, srv
}

func oidcTxFromRecorder(t *testing.T, rec *httptest.ResponseRecorder, name string) oidcTx {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != name {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(c.Value)
		if err != nil {
			t.Fatalf("cookie %s base64 解码失败: %v", name, err)
		}
		var tx oidcTx
		if err := json.Unmarshal(raw, &tx); err != nil {
			t.Fatalf("cookie %s JSON 解码失败: %v", name, err)
		}
		return tx
	}
	t.Fatalf("响应缺少 cookie %s：%v", name, rec.Result().Cookies())
	return oidcTx{}
}

func oidcLoginShape(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		if k == "state" || k == "nonce" || k == "code_challenge" {
			b.WriteString(k + "=<random>;")
			continue
		}
		b.WriteString(k + "=" + q.Get(k) + ";")
	}
	return b.String()
}

// TestOidcLoginIgnoresTurnstile：管理端 SSO 无 cf_turnstile_response 也应进入正常
// OIDC 授权码流程；带/不带 token 行为一致，且 Turnstile 参数不会被转发给 IdP。
func TestOidcLoginIgnoresTurnstile(t *testing.T) {
	h, idp := newOIDCFlowTestHandlers(t, "configured-turnstile-secret")
	cases := []struct{ name, query string }{
		{"无 token", ""},
		{"空 token", "?cf_turnstile_response="},
		{"有 token", "?cf_turnstile_response=whatever-token"},
	}
	shapes := make([]string, 0, len(cases))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/oidc/login"+tc.query, nil)
			h.oidcLogin(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("应 302 跳转 IdP，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if strings.Contains(loc, "oidc_error=") {
				t.Fatalf("不应返回 Turnstile/其它 OIDC 错误：%s", loc)
			}
			if !strings.HasPrefix(loc, idp.URL+"/authorize?") {
				t.Fatalf("应跳转到 discovery 的 authorization_endpoint，实际 %s", loc)
			}
			u, err := url.Parse(loc)
			if err != nil {
				t.Fatalf("Location 非法：%v", err)
			}
			q := u.Query()
			if _, ok := q["cf_turnstile_response"]; ok {
				t.Fatal("cf_turnstile_response 不应被转发给 IdP")
			}
			if q.Get("client_id") != "test-client" || q.Get("response_type") != "code" {
				t.Fatalf("OIDC 授权参数缺失：%s", q.Encode())
			}
			if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
				t.Fatalf("state/nonce/PKCE 必须保留：%s", q.Encode())
			}
			if got := q.Get("redirect_uri"); got != "https://panel.example.com/api/oidc/callback" {
				t.Fatalf("redirect_uri = %q", got)
			}
			tx := oidcTxFromRecorder(t, rec, oidcTxCookieAdmin)
			if tx.Mode != oidcModeAdmin {
				t.Fatalf("cookie mode = %q，期望 %q", tx.Mode, oidcModeAdmin)
			}
			if tx.State != q.Get("state") || tx.Nonce != q.Get("nonce") {
				t.Fatal("cookie 中 state/nonce 与授权请求不一致")
			}
			if pkceChallenge(tx.Verifier) != q.Get("code_challenge") {
				t.Fatal("PKCE verifier 与 code_challenge 不匹配")
			}
			shapes = append(shapes, oidcLoginShape(q))
		})
	}
	for i := 1; i < len(shapes); i++ {
		if shapes[i] != shapes[0] {
			t.Fatalf("带/不带 Turnstile token 的 OIDC 请求参数不一致：\n%s\n%s", shapes[0], shapes[i])
		}
	}
}
