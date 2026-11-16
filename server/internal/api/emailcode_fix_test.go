package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
// 邮件验证码发送链路（task-38）：限速/未配置/发信失败/成功/枚举统一响应
// =====================================================================

func emailTestHandlers(t *testing.T, cfg config.Config) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	h := &handlers{
		cfg:      cfg,
		db:       db,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		settings: map[string]string{},
	}
	return h, fdb
}

// emailHandlerEnv 安装通用的 DB 路由：app_settings 为空、限速无记录、发码写库成功。
func emailHandlerEnv(t *testing.T, fdb *fakeDB, adminEmail string, bound int64) {
	t.Helper()
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM app_settings"):
			return &fakeRows{}, nil
		case strings.Contains(q, "FROM admin_users WHERE LOWER(TRIM(email))"):
			if adminEmail == "" {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"username", "mfa", "secret"},
				[]driver.Value{"admin", int64(0), ""}), nil
		case strings.Contains(q, "FROM admin_email_codes") && strings.Contains(q, "MAX(created_at)"):
			return fakeRowsSet([]string{"last"}, []driver.Value{int64(0)}), nil
		case strings.Contains(q, "FROM admin_email_codes") && strings.Contains(q, "COUNT(*)"):
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		case strings.Contains(q, "FROM admin_email_codes"):
			// checkEmailCodeRate 的 last created_at 查询：无记录
			return &fakeRows{}, nil
		case strings.Contains(q, "FROM uuid_info") && strings.Contains(q, "COUNT(*)"):
			return fakeRowsSet([]string{"n"}, []driver.Value{bound}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
}

func smtpTestConfig() config.Config {
	return config.Config{
		JWTSecret:      "test-secret",
		SuperAdmin:     "admin",
		AdminTokenTTL:  time.Hour,
		PortalTokenTTL: time.Hour,
		MailProvider:   "smtp",
		SMTPAddr:       "smtp.example.com:587",
		MailFrom:       "noreply@example.com",
	}
}

func postJSON(t *testing.T, h http.HandlerFunc, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec, decodeTestJSON(t, rec.Body.Bytes())
}

func decodeTestJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	out := map[string]any{}
	if len(b) == 0 {
		return out
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("响应不是 JSON：%s", string(b))
	}
	return out
}

// ---- Turnstile ----

func turnstileTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	origURL, origClient := turnstileSiteverifyURL, turnstileHTTPClient
	turnstileSiteverifyURL = srv.URL
	turnstileHTTPClient = srv.Client()
	t.Cleanup(func() {
		turnstileSiteverifyURL, turnstileHTTPClient = origURL, origClient
		srv.Close()
	})
	return srv
}

func TestTurnstileFailOpenAndReject(t *testing.T) {
	h := &handlers{cfg: config.Config{TurnstileSecret: "secret"}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	req := httptest.NewRequest(http.MethodPost, "/", nil)

	// 成功
	turnstileTestServer(t, 200, `{"success":true}`)
	if err := h.verifyTurnstile(req, "tok"); err != nil {
		t.Fatalf("成功应放行：%v", err)
	}
	// 用户 token 无效 → 拒绝
	turnstileTestServer(t, 200, `{"success":false,"error-codes":["invalid-input-response"]}`)
	if err := h.verifyTurnstile(req, "tok"); err == nil {
		t.Fatal("invalid-input-response 应拒绝")
	}
	// secret 配置错误 → 失败开放（避免配置失误锁死登录）
	turnstileTestServer(t, 200, `{"success":false,"error-codes":["invalid-input-secret"]}`)
	if err := h.verifyTurnstile(req, "tok"); err != nil {
		t.Fatalf("invalid-input-secret 应失败开放：%v", err)
	}
	// Cloudflare 5xx → 失败开放
	turnstileTestServer(t, 503, `oops`)
	if err := h.verifyTurnstile(req, "tok"); err != nil {
		t.Fatalf("Turnstile 5xx 应失败开放：%v", err)
	}
	// 未配置 secret → 直接放行
	h2 := &handlers{cfg: config.Config{}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := h2.verifyTurnstile(req, ""); err != nil {
		t.Fatalf("未配置 secret 应放行：%v", err)
	}
}

// ---- 限速 ----

func TestCheckEmailCodeRate(t *testing.T) {
	h, fdb := emailTestHandlers(t, config.Config{})
	email := "a@example.com"

	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "ORDER BY id DESC") {
			return fakeRowsSet([]string{"created_at"}, []driver.Value{nowMillis() - 30_000}), nil
		}
		return &fakeRows{}, nil
	}
	if code, msg := h.checkEmailCodeRate(context.Background(), email, emailLoginPurpose); code != http.StatusTooManyRequests || !strings.Contains(msg, "秒后重试") {
		t.Fatalf("60 秒限速应 429：code=%d msg=%q", code, msg)
	}

	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "ORDER BY id DESC") {
			return &fakeRows{}, nil
		}
		if strings.Contains(q, "COUNT(*)") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(emailCodeHourly)}), nil
		}
		return &fakeRows{}, nil
	}
	if code, _ := h.checkEmailCodeRate(context.Background(), email, emailLoginPurpose); code != http.StatusTooManyRequests {
		t.Fatalf("每小时限速应 429，得到 %d", code)
	}
}

// issueEmailCode 内置限速应返回类型化错误（handler 可转成 429）。
func TestIssueEmailCodeRateTyped(t *testing.T) {
	h, fdb := emailTestHandlers(t, config.Config{})
	now := nowMillis()
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "MAX(created_at)") {
			return fakeRowsSet([]string{"last"}, []driver.Value{now - 10_000}), nil
		}
		if strings.Contains(q, "COUNT(*)") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	if _, err := h.issueEmailCode(context.Background(), "a@example.com", "admin", emailLoginPurpose); err == nil {
		t.Fatal("应触发限速")
	} else {
		var rate *emailCodeRateError
		if !errors.As(err, &rate) {
			t.Fatalf("限速错误类型不对：%T %v", err, err)
		}
	}
}

// ---- 邮件预检 ----

func TestMailPreflight(t *testing.T) {
	h, _ := emailTestHandlers(t, config.Config{})
	if err := h.mailPreflight(context.Background()); err == nil {
		t.Fatal("未配置邮件应报错")
	}
	h.cfg = smtpTestConfig()
	if err := h.mailPreflight(context.Background()); err != nil {
		t.Fatalf("SMTP 就绪应通过：%v", err)
	}
}

// ---- 管理端登录邮件验证码 ----

func TestLoginEmailCodeBoundSuccessAndUniform(t *testing.T) {
	h, fdb := emailTestHandlers(t, smtpTestConfig())
	var sent int
	orig := mailDeliver
	mailDeliver = func(h *handlers, to, subject, body string) error { sent++; return nil }
	t.Cleanup(func() { mailDeliver = orig })

	emailHandlerEnv(t, fdb, "admin", 0)
	rec, body := postJSON(t, h.loginEmailCodeSend, "/api/login/email-code", `{"email":"admin@example.com"}`)
	if rec.Code != http.StatusOK || body["success"] != true {
		t.Fatalf("已绑定管理员应统一 200：status=%d body=%s", rec.Code, rec.Body.String())
	}
	if sent != 1 {
		t.Fatalf("应发出 1 封邮件，实际 %d", sent)
	}
	if body["request_id"] == "" || body["request_id"] == nil {
		t.Fatal("响应应带 request_id")
	}
	boundMsg, _ := body["message"].(string)

	// 未绑定：同样的 200/相同统一文案，且不投递邮件（不泄露枚举）。
	emailHandlerEnv(t, fdb, "", 0)
	rec2, body2 := postJSON(t, h.loginEmailCodeSend, "/api/login/email-code", `{"email":"nobody@example.com"}`)
	if rec2.Code != http.StatusOK || sent != 1 {
		t.Fatalf("未绑定不应投递：status=%d sent=%d", rec2.Code, sent)
	}
	if body2["message"] != boundMsg {
		t.Fatalf("绑定/未绑定文案必须一致，避免枚举：%q vs %q", body2["message"], boundMsg)
	}
}

func TestLoginEmailCodeMailFailureNotSilent(t *testing.T) {
	h, fdb := emailTestHandlers(t, smtpTestConfig())
	orig := mailDeliver
	mailDeliver = func(h *handlers, to, subject, body string) error {
		return errors.New("dial tcp 1.2.3.4:25: connection refused")
	}
	t.Cleanup(func() { mailDeliver = orig })
	emailHandlerEnv(t, fdb, "admin", 0)
	rec, body := postJSON(t, h.loginEmailCodeSend, "/api/login/email-code", `{"email":"admin@example.com"}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("发信失败应 502，得到 %d", rec.Code)
	}
	msg, _ := body["error"].(string)
	if !strings.Contains(msg, "邮件服务暂时不可用") || strings.Contains(msg, "connection refused") {
		t.Fatalf("错误文案应可重试且不泄露细节：%q", msg)
	}
	if body["request_id"] == nil {
		t.Fatal("502 应带 request_id")
	}
}

func TestLoginEmailCodeMailNotConfigured(t *testing.T) {
	h, fdb := emailTestHandlers(t, config.Config{JWTSecret: "s", SuperAdmin: "admin"})
	emailHandlerEnv(t, fdb, "admin", 0)
	rec, _ := postJSON(t, h.loginEmailCodeSend, "/api/login/email-code", `{"email":"admin@example.com"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("邮件未配置应 503，得到 %d", rec.Code)
	}
}

func TestLoginEmailCodeRateLimited(t *testing.T) {
	h, fdb := emailTestHandlers(t, smtpTestConfig())
	emailHandlerEnv(t, fdb, "admin", 0)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM app_settings") {
			return &fakeRows{}, nil
		}
		if strings.Contains(q, "ORDER BY id DESC") {
			return fakeRowsSet([]string{"created_at"}, []driver.Value{nowMillis() - 5_000}), nil
		}
		return &fakeRows{}, nil
	}
	rec, body := postJSON(t, h.loginEmailCodeSend, "/api/login/email-code", `{"email":"admin@example.com"}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("限速应 429，得到 %d body=%s", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "秒后重试") {
		t.Fatalf("限速文案应带等待秒数：%q", msg)
	}
}

// ---- 用户端 send-code ----

func TestPortalSendCodeUniformAndMailFailure(t *testing.T) {
	h, fdb := emailTestHandlers(t, smtpTestConfig())
	var sent int
	orig := mailDeliver
	mailDeliver = func(h *handlers, to, subject, body string) error { sent++; return nil }
	t.Cleanup(func() { mailDeliver = orig })

	// 已绑定：200 + 统一文案 + 投递
	emailHandlerEnv(t, fdb, "", 1)
	rec, body := postJSON(t, h.portalSendCode, "/api/portal/send-code", `{"email":"yang@example.com"}`)
	if rec.Code != http.StatusOK || sent != 1 {
		t.Fatalf("已绑定应 200 且投递：status=%d sent=%d", rec.Code, sent)
	}
	boundMsg, _ := body["message"].(string)
	if boundMsg == "" || body["request_id"] == nil {
		t.Fatalf("应返回统一文案与 request_id：%s", rec.Body.String())
	}

	// 未绑定：相同文案、不投递
	emailHandlerEnv(t, fdb, "", 0)
	rec2, body2 := postJSON(t, h.portalSendCode, "/api/portal/send-code", `{"email":"nobody@example.com"}`)
	if rec2.Code != http.StatusOK || sent != 1 {
		t.Fatalf("未绑定不应投递：status=%d sent=%d", rec2.Code, sent)
	}
	if body2["message"] != boundMsg {
		t.Fatalf("绑定/未绑定文案必须一致：%q vs %q", body2["message"], boundMsg)
	}

	// 发信失败：502 可区分重试提示
	mailDeliver = func(h *handlers, to, subject, body string) error { return errors.New("mx timeout") }
	emailHandlerEnv(t, fdb, "", 1)
	rec3, body3 := postJSON(t, h.portalSendCode, "/api/portal/send-code", `{"email":"yang@example.com"}`)
	if rec3.Code != http.StatusBadGateway {
		t.Fatalf("发信失败应 502，得到 %d", rec3.Code)
	}
	if msg, _ := body3["error"].(string); !strings.Contains(msg, "邮件服务暂时不可用") {
		t.Fatalf("错误文案不清晰：%q", msg)
	}
}

func TestPortalSendCodeMailNotConfigured(t *testing.T) {
	h, fdb := emailTestHandlers(t, config.Config{})
	emailHandlerEnv(t, fdb, "", 1)
	rec, _ := postJSON(t, h.portalSendCode, "/api/portal/send-code", `{"email":"yang@example.com"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("邮件未配置应 503，得到 %d", rec.Code)
	}
}

// 邮箱查询必须使用 TRIM，避免库里前后空格导致「已绑定却查不到」。
func TestEmailLookupUsesTrim(t *testing.T) {
	h, fdb := emailTestHandlers(t, config.Config{})
	var query string
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		query = q
		return &fakeRows{}, nil
	}
	_, _ = h.bindingByEmail(context.Background(), "a@example.com")
	if !strings.Contains(query, "LOWER(TRIM(email))") {
		t.Fatalf("查询未使用 TRIM：%s", query)
	}
}

// 绑定邮箱入库前必须校验/规范化：历史上 `xxx@@domain` 这类脏数据会让用户端
// 邮箱验证码登录永远查不到绑定（task-38 用户补充反馈：新建用户表单缺校验）。
func TestUUIDSaveEmailValidation(t *testing.T) {
	h, fdb := emailTestHandlers(t, smtpTestConfig())
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }

	// 非法邮箱 → 400，不写库
	rec, body := postJSON(t, h.uuidsSave, "/api/uuids", `{"uuid":"11111111-1111-4111-8111-111111111111","email":"bad@@example.com"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法邮箱应 400，得到 %d body=%s", rec.Code, rec.Body.String())
	}
	if msg, _ := body["error"].(string); !strings.Contains(msg, "邮箱格式不正确") {
		t.Fatalf("错误文案不清晰：%q", msg)
	}
	if fdb.countExec("INSERT INTO uuid_info") != 0 {
		t.Fatal("非法邮箱不应写库")
	}

	// 合法邮箱带大小写/空格 → 规范化后写入（trim+lower）
	rec2, _ := postJSON(t, h.uuidsSave, "/api/uuids", `{"uuid":"22222222-2222-4222-8222-222222222222","email":"  Yang@Example.COM  "}`)
	if rec2.Code != http.StatusOK {
		t.Fatalf("合法邮箱应保存成功，得到 %d body=%s", rec2.Code, rec2.Body.String())
	}
	call, ok := fdb.lastExec("INSERT INTO uuid_info")
	if !ok {
		t.Fatal("应写入 uuid_info")
	}
	found := false
	for _, arg := range call.Args {
		if s, ok := arg.(string); ok && s == "yang@example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("邮箱未规范化入库：%v", call.Args)
	}
}
