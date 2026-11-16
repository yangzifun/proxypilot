package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"
)

const (
	emailCodeTTL      = 10 * time.Minute
	emailCodeResend   = 60 * time.Second
	emailCodeHourly   = 10
	emailCodeAttempts = 5
)

// emailCodeRateError 表示验证码发送触发限速；Wait 是建议重试秒数。
type emailCodeRateError struct {
	Wait int
}

func (e *emailCodeRateError) Error() string {
	if e.Wait > 0 {
		return fmt.Sprintf("发送过于频繁，请 %d 秒后重试", e.Wait)
	}
	return "验证码发送过于频繁，请稍后再试"
}

var (
	errCodeNotFound = errors.New("请先获取验证码")
	errCodeExpired  = errors.New("验证码已过期，请重新获取")
	errCodeTooMany  = errors.New("尝试次数过多，请重新获取验证码")
	errCodeWrong    = errors.New("验证码不正确")
)

var emailRe = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

func normalizeEmail(raw string) string {
	e := strings.ToLower(strings.TrimSpace(raw))
	if e == "" || len(e) > 254 || !emailRe.MatchString(e) {
		return ""
	}
	return e
}

func generateEmailCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// hashEmailCode 与 Worker 完全一致：hex(sha256("purpose:email:code"))
// 这样迁移期间未过期的验证码仍然可用。
func hashEmailCode(email, code, purpose string) string {
	sum := sha256.Sum256([]byte(purpose + ":" + email + ":" + code))
	return hex.EncodeToString(sum[:])
}

// issueEmailCode 作废旧码并写入新码（同一事务）。
func (h *handlers) issueEmailCode(ctx context.Context, email, username, purpose string) (string, error) {
	code := generateEmailCode()
	now := nowMillis()
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var lockID int64
	_ = tx.QueryRowContext(ctx, `SELECT id FROM admin_email_codes WHERE email = ? AND purpose = ? ORDER BY id DESC LIMIT 1 FOR UPDATE`, email, purpose).Scan(&lockID)
	var lastCreated, hourCount int64
	_ = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(created_at),0) FROM admin_email_codes WHERE email = ? AND purpose = ?`, email, purpose).Scan(&lastCreated)
	_ = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_email_codes WHERE email = ? AND purpose = ? AND created_at > ?`, email, purpose, now-3600*1000).Scan(&hourCount)
	if lastCreated > 0 && now-lastCreated < 60*1000 {
		wait := int((60*1000 - (now - lastCreated)) / 1000)
		if wait < 1 {
			wait = 1
		}
		return "", &emailCodeRateError{Wait: wait}
	}
	if hourCount >= 10 {
		return "", &emailCodeRateError{}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE admin_email_codes SET used = 1 WHERE email = ? AND purpose = ? AND used = 0`,
		email, purpose); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_email_codes (username, email, code_hash, purpose, attempts, expires_at, used, created_at)
		 VALUES (?, ?, ?, ?, 0, ?, 0, ?)`,
		username, email, hashEmailCode(email, code, purpose), purpose,
		now+emailCodeTTL.Milliseconds(), now); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return code, nil
}

// consumeEmailCode 校验并一次性消费验证码。
func (h *handlers) consumeEmailCode(ctx context.Context, email, code, purpose string) (bool, int, string) {
	var (
		id        int64
		hash      string
		expiresAt int64
		attempts  int64
	)
	err := h.db.QueryRowContext(ctx,
		`SELECT id, code_hash, COALESCE(expires_at,0), COALESCE(attempts,0)
		   FROM admin_email_codes
		  WHERE email = ? AND purpose = ? AND used = 0
		  ORDER BY id DESC LIMIT 1`, email, purpose).
		Scan(&id, &hash, &expiresAt, &attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 400, errCodeNotFound.Error()
	}
	if err != nil {
		return false, 500, "服务器错误"
	}
	if expiresAt < nowMillis() {
		return false, 400, errCodeExpired.Error()
	}
	if attempts >= emailCodeAttempts {
		_, _ = h.db.ExecContext(ctx, `UPDATE admin_email_codes SET used = 1 WHERE id = ? AND used = 0`, id)
		return false, 429, errCodeTooMany.Error()
	}
	if hashEmailCode(email, code, purpose) != hash {
		_, _ = h.db.ExecContext(ctx, `UPDATE admin_email_codes SET attempts = attempts + 1 WHERE id = ?`, id)
		return false, 401, errCodeWrong.Error()
	}
	res, cerr := h.db.ExecContext(ctx, `UPDATE admin_email_codes SET used = 1 WHERE id = ? AND used = 0`, id)
	if cerr != nil {
		return false, 500, "服务器错误"
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, 401, errCodeWrong.Error()
	}
	return true, 200, ""
}

// checkEmailCodeRate 复刻 Worker 的频率限制：60 秒内一条、每小时 10 条。
func (h *handlers) checkEmailCodeRate(ctx context.Context, email, purpose string) (int, string) {
	var last sql.NullInt64
	_ = h.db.QueryRowContext(ctx,
		`SELECT created_at FROM admin_email_codes WHERE email = ? AND purpose = ?
		  ORDER BY id DESC LIMIT 1`, email, purpose).Scan(&last)
	if last.Valid && nowMillis()-last.Int64 < emailCodeResend.Milliseconds() {
		wait := (emailCodeResend.Milliseconds() - (nowMillis() - last.Int64)) / 1000
		if wait < 1 {
			wait = 1
		}
		return 429, fmt.Sprintf("发送过于频繁，请 %d 秒后重试", wait)
	}
	var hourly int64
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_email_codes
		  WHERE email = ? AND purpose = ? AND created_at > ?`,
		email, purpose, nowMillis()-3_600_000).Scan(&hourly)
	if hourly >= emailCodeHourly {
		return 429, "验证码发送过于频繁，请稍后再试"
	}
	return 0, ""
}

// fullEmail 是 portal_session 用到的邮箱绑定查询结果。
type portalBinding struct {
	UUID  string
	Email string
}

// bindingByEmail 查邮箱绑定的 UUID（大小写不敏感）。
func (h *handlers) bindingByEmail(ctx context.Context, email string) (portalBinding, bool) {
	var b portalBinding
	err := h.db.QueryRowContext(ctx,
		`SELECT uuid, email FROM uuid_info WHERE LOWER(TRIM(email)) = ? ORDER BY id ASC LIMIT 1`, email).
		Scan(&b.UUID, &b.Email)
	if err != nil {
		return b, false
	}
	return b, true
}

// emailTakenBy 判断邮箱是否已被其它 UUID 占用。
func (h *handlers) emailTakenBy(ctx context.Context, email, exceptUUID string) (string, bool) {
	if email == "" {
		return "", false
	}
	var uuid string
	err := h.db.QueryRowContext(ctx,
		`SELECT uuid FROM uuid_info WHERE LOWER(TRIM(email)) = ? AND uuid <> ? LIMIT 1`, email, exceptUUID).Scan(&uuid)
	if err != nil {
		return "", false
	}
	return uuid, true
}

// ---- 发信辅助：配置预检 / 重试 / 用户安全文案 / request_id ----

// mailDeliver 是统一发信注入点（单测可替换）。
var mailDeliver = func(h *handlers, to, subject, body string) error {
	return h.sendMail(to, subject, body)
}

// newRequestID 生成短 request_id，用于把用户看到的错误与服务端日志对上。
func newRequestID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("r%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// mailPreflight 在真正查账号/发码之前检查邮件通道是否可用。
// 这是全局配置问题，返回的 503 不会泄露「某个邮箱是否已绑定」。
func (h *handlers) mailPreflight(ctx context.Context) error {
	mc := h.effectiveMail(ctx)
	switch mc.ResolvedProvider() {
	case "builtin":
		if !mc.BuiltinReady() {
			return errMailNotConfigured
		}
		return nil
	case "smtp":
		if !mc.SMTPReady() {
			return errMailNotConfigured
		}
		return nil
	default:
		return errMailNotConfigured
	}
}

// deliverMailWithRetry 发信失败重试一次；返回 (用户安全文案, 原始错误)。
// 登录验证码场景不能把 SMTP/核心错误原文暴露给客户端。
func (h *handlers) deliverMailWithRetry(to, subject, body string) (string, error) {
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		last = mailDeliver(h, to, subject, body)
		if last == nil {
			return "", nil
		}
		if errors.Is(last, errMailNotConfigured) {
			return "邮件服务未配置，请联系管理员", last
		}
		if attempt == 0 {
			time.Sleep(150 * time.Millisecond)
		}
	}
	return "邮件服务暂时不可用，请稍后重试", last
}
