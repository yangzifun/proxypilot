package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 造一个带指定 cookie 的请求
func reqWithCookies(pairs map[string]oidcTx) *http.Request {
	r := httptest.NewRequest("GET", "/api/oidc/callback?state=s1", nil)
	for name, tx := range pairs {
		r.AddCookie(oidcTxCookieNamed(r, name, tx, 600))
	}
	return r
}

// 核心回归：用户中心流程必须选中 portal 事务，绝不能落到 admin。
func TestPickOidcTxPrefersPortal(t *testing.T) {
	r := reqWithCookies(map[string]oidcTx{
		oidcTxCookiePortal: {State: "s1", Verifier: "v", Mode: oidcModePortal},
	})
	tx, mode, ok := pickOidcTx(r, "s1")
	if !ok {
		t.Fatal("应该选中 portal 事务")
	}
	if mode != oidcModePortal {
		t.Fatalf("渠道应为 portal，实际 %q（这就是「SSO 登录后跳管理端」的根因）", mode)
	}
	if tx.Verifier != "v" {
		t.Fatalf("verifier 丢失：%q", tx.Verifier)
	}
}

// 两个 cookie 同时存在且 state 相同（同浏览器先后试过两端）时，也应选 portal。
func TestPickOidcTxPortalWinsWhenBothPresent(t *testing.T) {
	r := reqWithCookies(map[string]oidcTx{
		oidcTxCookieAdmin:  {State: "s1", Mode: oidcModeAdmin},
		oidcTxCookiePortal: {State: "s1", Mode: oidcModePortal},
	})
	_, mode, ok := pickOidcTx(r, "s1")
	if !ok || mode != oidcModePortal {
		t.Fatalf("应选 portal，实际 ok=%v mode=%q", ok, mode)
	}
}

// 管理端流程保持 admin。
func TestPickOidcTxAdmin(t *testing.T) {
	r := reqWithCookies(map[string]oidcTx{
		oidcTxCookieAdmin: {State: "s2", Mode: oidcModeAdmin},
	})
	_, mode, ok := pickOidcTx(r, "s2")
	if !ok || mode != oidcModeAdmin {
		t.Fatalf("应选 admin，实际 ok=%v mode=%q", ok, mode)
	}
}

// state 对不上时不能选中任何事务。
func TestPickOidcTxStateMismatch(t *testing.T) {
	r := reqWithCookies(map[string]oidcTx{
		oidcTxCookiePortal: {State: "other", Mode: oidcModePortal},
	})
	if _, _, ok := pickOidcTx(r, "s1"); ok {
		t.Fatal("state 不匹配时不应选中")
	}
	if _, _, ok := pickOidcTx(r, ""); ok {
		t.Fatal("空 state 不应选中")
	}
}

// cookie 里没有 mode 的老事务，按 state 后缀推断。
func TestPickOidcTxModeFromStateFallback(t *testing.T) {
	r := reqWithCookies(map[string]oidcTx{
		oidcTxCookiePortal: {State: "s3." + oidcModePortal},
	})
	_, mode, ok := pickOidcTx(r, "s3."+oidcModePortal)
	if !ok || mode != oidcModePortal {
		t.Fatalf("没有 mode 时应从 state 推断成 portal，实际 ok=%v mode=%q", ok, mode)
	}
}
