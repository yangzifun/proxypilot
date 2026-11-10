package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// state 里必须带渠道，否则回调拿不到 cookie 时无法判断该回哪个登录页。
func TestOidcStateCarriesMode(t *testing.T) {
	for _, mode := range []string{oidcModeAdmin, oidcModePortal} {
		st := oidcState(mode)
		if !strings.HasSuffix(st, "."+mode) {
			t.Fatalf("state %q 应以 .%s 结尾", st, mode)
		}
		if got := oidcStateMode(st); got != mode {
			t.Fatalf("state %q 解析出 %q，期望 %q", st, got, mode)
		}
	}
}

// 老链接（没有后缀）按管理端处理，保持兼容。
func TestOidcStateModeLegacy(t *testing.T) {
	if got := oidcStateMode("plainrandomstate"); got != oidcModeAdmin {
		t.Fatalf("无后缀 state 应回落到 admin，实际 %q", got)
	}
	if got := oidcStateMode(""); got != oidcModeAdmin {
		t.Fatalf("空 state 应回落到 admin，实际 %q", got)
	}
}

// 关键回归：cookie 的 Path 必须是 /api。
// 之前写成 /api/oidc，而用户中心从 /api/portal/oidc/login 发起，
// 浏览器会丢掉这个 Set-Cookie，导致回调判定成管理员流程 → 跳到管理端。
func TestOidcTxCookiePathCoversPortal(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/portal/oidc/login", nil)

	admin := oidcTxCookie(r, oidcTx{State: "s", Mode: oidcModeAdmin}, 600)
	portal := portalOidcTxCookie(r, oidcTx{State: "s", Mode: oidcModePortal}, 600)

	for _, c := range []*http.Cookie{admin, portal} {
		if c.Path != "/api" {
			t.Fatalf("cookie %s 的 Path=%q，必须是 /api（否则 /api/portal/oidc/login 设置的 cookie 会被浏览器丢弃）", c.Name, c.Path)
		}
		if !strings.HasPrefix("/api/oidc/callback", c.Path) {
			t.Fatalf("Path %q 覆盖不到回调地址", c.Path)
		}
		if !strings.HasPrefix("/api/portal/oidc/login", c.Path) {
			t.Fatalf("Path %q 覆盖不到用户中心发起地址", c.Path)
		}
	}

	// 两个渠道的 cookie 名必须不同，否则同浏览器先后尝试会互相覆盖
	if admin.Name == portal.Name {
		t.Fatalf("管理端与用户中心不能共用 cookie 名 %q", admin.Name)
	}
}

// 清 cookie 时要把两个都清掉，且 Path 与写入时一致（否则清不掉）。
func TestClearOidcTxClearsBoth(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/oidc/callback", nil)
	w := httptest.NewRecorder()
	clearOidcTx(w, r)

	got := map[string]bool{}
	for _, line := range w.Result().Header.Values("Set-Cookie") {
		for _, name := range []string{oidcTxCookieAdmin, oidcTxCookiePortal} {
			if strings.HasPrefix(line, name+"=") {
				got[name] = true
				if !strings.Contains(line, "Path=/api") {
					t.Fatalf("%s 的清除 cookie 缺少 Path=/api：%s", name, line)
				}
				if !strings.Contains(line, "Max-Age=0") && !strings.Contains(line, "Max-Age=-1") {
					t.Fatalf("%s 的清除 cookie 没有失效标记：%s", name, line)
				}
			}
		}
	}
	for _, name := range []string{oidcTxCookieAdmin, oidcTxCookiePortal} {
		if !got[name] {
			t.Fatalf("没有清除 %s", name)
		}
	}
}

// oidcFail 必须按渠道回跳到对应登录页。
func TestOidcFailTargetsRightPage(t *testing.T) {
	cases := map[string]string{
		oidcModeAdmin:  "/login?oidc_error=",
		oidcModePortal: "/?oidc_error=",
	}
	for mode, prefix := range cases {
		r := httptest.NewRequest("GET", "/api/oidc/callback?state=x&error=foo", nil)
		w := httptest.NewRecorder()
		oidcFail(w, r, mode, "no_uuid_binding")
		loc := w.Result().Header.Get("Location")
		if !strings.HasPrefix(loc, prefix) {
			t.Fatalf("mode=%s 应跳到 %s…，实际 %q", mode, prefix, loc)
		}
		if strings.Contains(loc, "/admin") {
			t.Fatalf("mode=%s 不能跳到管理端，实际 %q", mode, loc)
		}
	}
}