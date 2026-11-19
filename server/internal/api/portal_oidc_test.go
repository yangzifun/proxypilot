package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestPortalOIDCLoginIgnoresTurnstile：用户中心 SSO 同样不再要求人机验证，
// 无 token 也能进入 OIDC 授权码流程；带/不带 token 参数一致。
func TestPortalOIDCLoginIgnoresTurnstile(t *testing.T) {
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
			req := httptest.NewRequest(http.MethodGet, "/api/portal/oidc/login"+tc.query, nil)
			h.portalOIDCLogin(rec, req)

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
			tx := oidcTxFromRecorder(t, rec, oidcTxCookiePortal)
			if tx.Mode != oidcModePortal {
				t.Fatalf("cookie mode = %q，期望 %q", tx.Mode, oidcModePortal)
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

// TestOIDCCallbackStillRequiresState：移除 Turnstile 后，回调仍必须校验 state
// （没有 tx cookie 时必须回登录页报 state_mismatch，不能继续换 token）。
func TestOIDCCallbackStillRequiresState(t *testing.T) {
	h, _ := newOIDCFlowTestHandlers(t, "configured-turnstile-secret")
	cases := []struct {
		mode        string
		errorPrefix string
	}{
		{oidcModeAdmin, "/login?oidc_error="},
		{oidcModePortal, "/?oidc_error="},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			state := oidcState(tc.mode)
			// 故意不带 state 对应的 tx cookie
			req := httptest.NewRequest(http.MethodGet,
				"/api/oidc/callback?code=abc&state="+url.QueryEscape(state), nil)
			rec := httptest.NewRecorder()
			h.oidcCallback(rec, req)
			if rec.Code != http.StatusFound {
				t.Fatalf("应 302 回登录页，实际 %d body=%s", rec.Code, rec.Body.String())
			}
			loc := rec.Header().Get("Location")
			if !strings.HasPrefix(loc, tc.errorPrefix) || !strings.Contains(loc, "state_mismatch") {
				t.Fatalf("缺少 state 时应回 %sstate_mismatch，实际 %q", tc.errorPrefix, loc)
			}
		})
	}
}
