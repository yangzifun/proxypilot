package api

import (
	"database/sql/driver"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// 用户中心「密码登录」的 Turnstile 单测。
//
// 不联网：需要「校验通过 / 校验失败」的分支通过替换包级变量
// portalTurnstileVerify 注入假实现；「未配置 secret」与「缺 token」两条
// 分支走真实 verifyTurnstile，但都在发 HTTP 之前就返回，同样不联网。

const (
	portalPwdTestUUID   = "11111111-2222-3333-4444-555555555555"
	portalPwdTestHashPw = "CorrectHorse9"
	portalPwdTestSecret = "turnstile-test-secret"
	portalPwdTestJWT    = "portal-password-test-jwt-secret"
)

// newPortalPasswordTestHandlers 构造带 fake DB 的 handlers：
// uuid_info 查询固定返回一行已设置密码的用户；其它查询返回空结果集，
// 于是 app_settings 为空、turnstileSecret 回落到 cfg.TurnstileSecret。
func newPortalPasswordTestHandlers(t *testing.T, turnstileSecret string) (*handlers, *fakeDB) {
	t.Helper()
	hash, err := auth.HashPassword(portalPwdTestHashPw)
	if err != nil {
		t.Fatalf("生成口令哈希失败：%v", err)
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM uuid_info") {
			return fakeRowsSet(
				[]string{"uuid", "email", "password_hash", "password_must_change"},
				[]driver.Value{portalPwdTestUUID, "pwd-test@example.com", hash, int64(0)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{
			JWTSecret:       portalPwdTestJWT,
			PortalTokenTTL:  time.Hour,
			TurnstileSecret: turnstileSecret,
		},
	}
	return h, fdb
}

func callPortalLoginPassword(h *handlers, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/portal/login/password", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.portalLoginPassword(rec, req)
	return rec
}

// portalPasswordLookupCount 统计真正读取 uuid_info 口令行的次数。
func portalPasswordLookupCount(fdb *fakeDB) int {
	n := 0
	for _, c := range fdb.queryCalls() {
		if strings.Contains(c.Query, "FROM uuid_info") && strings.Contains(c.Query, "password_hash") {
			n++
		}
	}
	return n
}

// swapPortalTurnstileVerify 用假校验函数替换默认实现，测试结束后自动还原。
func swapPortalTurnstileVerify(t *testing.T, fn func(*handlers, *http.Request, string) error) {
	t.Helper()
	old := portalTurnstileVerify
	portalTurnstileVerify = fn
	t.Cleanup(func() { portalTurnstileVerify = old })
}

// 配置了 secret 但请求没带 token：403「请完成人机验证」，且不进入口令查询/校验。
func TestPortalLoginPasswordTurnstileMissingToken(t *testing.T) {
	h, fdb := newPortalPasswordTestHandlers(t, portalPwdTestSecret)

	rec := callPortalLoginPassword(h,
		`{"email":"missing-token@example.com","password":"`+portalPwdTestHashPw+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("缺少 token 应返回 403，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "请完成人机验证") {
		t.Fatalf("错误文案应为「请完成人机验证」，实际：%s", rec.Body.String())
	}
	if n := portalPasswordLookupCount(fdb); n != 0 {
		t.Fatalf("Turnstile 未通过时不应查询用户口令，实际查询 %d 次", n)
	}

	// 顺序约束：空密码属于参数错误，应先于 Turnstile 返回 400，而不是 403。
	rec = callPortalLoginPassword(h, `{"email":"missing-token@example.com","password":""}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空密码应先返回 400，实际 %d：%s", rec.Code, rec.Body.String())
	}
}

// 配置了 secret、Turnstile 校验失败：403「人机验证失败」，同样不查库。
func TestPortalLoginPasswordTurnstileRejected(t *testing.T) {
	h, fdb := newPortalPasswordTestHandlers(t, portalPwdTestSecret)
	swapPortalTurnstileVerify(t, func(_ *handlers, _ *http.Request, token string) error {
		if token != "bad-token" {
			t.Fatalf("校验函数应收到请求里的 token，实际 %q", token)
		}
		return errors.New("人机验证失败")
	})

	rec := callPortalLoginPassword(h,
		`{"email":"bad-token@example.com","password":"`+portalPwdTestHashPw+`","cf_turnstile_response":"bad-token"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("Turnstile 校验失败应返回 403，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "人机验证失败") {
		t.Fatalf("错误文案应为「人机验证失败」，实际：%s", rec.Body.String())
	}
	if n := portalPasswordLookupCount(fdb); n != 0 {
		t.Fatalf("Turnstile 校验失败时不应查询用户口令，实际查询 %d 次", n)
	}
}

// 配置了 secret、Turnstile 通过：密码正确则正常登录。
func TestPortalLoginPasswordTurnstilePassedSucceeds(t *testing.T) {
	h, _ := newPortalPasswordTestHandlers(t, portalPwdTestSecret)
	var seenToken string
	swapPortalTurnstileVerify(t, func(_ *handlers, _ *http.Request, token string) error {
		seenToken = token
		return nil
	})

	rec := callPortalLoginPassword(h,
		`{"email":"passed@example.com","password":"`+portalPwdTestHashPw+`","cf_turnstile_response":"good-token"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("Turnstile 通过且密码正确应登录成功，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if seenToken != "good-token" {
		t.Fatalf("校验函数应收到 cf_turnstile_response，实际 %q", seenToken)
	}
	body := rec.Body.String()
	for _, want := range []string{`"token":"`, `"uuid":"` + portalPwdTestUUID + `"`, `"must_change_password":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("成功响应缺少 %s：%s", want, body)
		}
	}
}

// 配置了 secret、Turnstile 通过但密码错误：仍是 401「邮箱或密码错误」。
func TestPortalLoginPasswordTurnstilePassedWrongPassword(t *testing.T) {
	h, _ := newPortalPasswordTestHandlers(t, portalPwdTestSecret)
	swapPortalTurnstileVerify(t, func(_ *handlers, _ *http.Request, _ string) error { return nil })

	rec := callPortalLoginPassword(h,
		`{"email":"wrong-pw@example.com","password":"WrongPassword9","cf_turnstile_response":"good-token"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("密码错误应返回 401，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "邮箱或密码错误") {
		t.Fatalf("错误文案应保持「邮箱或密码错误」，实际：%s", rec.Body.String())
	}
}

// 未配置 secret：不要求 token，保持旧环境兼容（不发 HTTP，走真实默认实现）。
func TestPortalLoginPasswordTurnstileNotConfigured(t *testing.T) {
	h, _ := newPortalPasswordTestHandlers(t, "")

	rec := callPortalLoginPassword(h,
		`{"email":"no-secret@example.com","password":"`+portalPwdTestHashPw+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("未配置 secret 时不应要求 token，实际 %d：%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"token":"`) {
		t.Fatalf("未配置 secret 时应正常签发 token：%s", rec.Body.String())
	}
}