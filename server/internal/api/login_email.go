package api

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

const emailLoginPurpose = "login_email"
const emailLoginTicketPurpose = "email_login_tx"

func (h *handlers) findAdminByVerifiedEmail(ctx context.Context, email string) (string, int64, string, bool) {
	var (
		username string
		mfa      int64
		secret   sql.NullString
	)
	err := h.db.QueryRowContext(ctx,
		`SELECT username, COALESCE(mfa_enabled,0), COALESCE(mfa_secret,'')
		   FROM admin_users WHERE LOWER(email) = ? AND COALESCE(email_verified,0) = 1 LIMIT 1`, email).
		Scan(&username, &mfa, &secret)
	if err != nil {
		return "", 0, "", false
	}
	return username, mfa, secret.String, true
}

// POST /api/login/email-code
func (h *handlers) loginEmailCodeSend(w http.ResponseWriter, r *http.Request) {
	if !h.emailCodeLoginEnabled(r.Context()) {
		writeError(w, http.StatusForbidden, "邮箱验证码登录已关闭")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	email := normalizeEmail(mapStr(body, "email", ""))
	if email == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	// 审计低-04：发送登录验证码必须过人机验证（与 /api/login、/api/portal/send-code
	// 保持一致），防止被用于邮件轰炸。
	if err := h.verifyTurnstile(r, mapStr(body, "cf_turnstile_response", "")); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	if code, msg := h.checkEmailCodeRate(r.Context(), email, emailLoginPurpose); code != 0 {
		writeError(w, code, msg)
		return
	}
	// 审计低-04：不再对未绑定邮箱返回 404（可枚举管理员邮箱）；统一响应。
	generic := map[string]any{
		"success":     true,
		"ttl_minutes": 10,
		"message":     "如果该邮箱已绑定管理员账号，验证码将发送到邮箱",
	}
	username, _, _, ok := h.findAdminByVerifiedEmail(r.Context(), email)
	if !ok {
		writeJSON(w, http.StatusOK, generic)
		return
	}
	code, err := h.issueEmailCode(r.Context(), email, username, emailLoginPurpose)
	if err != nil {
		h.log.Error("生成验证码失败", "err", err)
		writeJSON(w, http.StatusOK, generic)
		return
	}
	ip := clientIP(r)
	ua := strings.TrimSpace(r.UserAgent())
	if len(ua) > 180 {
		ua = ua[:180] + "…"
	}
	intro := fmt.Sprintf(`账号：<b>%s</b><br>时间：%s<br>登录 IP：%s<br>客户端：%s`,
		html.EscapeString(username), time.Now().Format("2006-01-02 15:04:05 MST"),
		html.EscapeString(ip), html.EscapeString(ua))
	if base := h.publicBaseURL(r.Context()); base != "" {
		intro += fmt.Sprintf(`<br>后台地址：<a href="%s">%s</a>`, html.EscapeString(base), html.EscapeString(base))
	}
	intro += `<br><br>请输入下面的 6 位验证码完成登录。如非本人操作，请立即修改密码并检查登录记录。`
	mailBody := emailHTML("管理后台登录验证码", intro, code, 10)
	if err := h.sendMail(email, "【代理配置管理】登录验证码", mailBody); err != nil {
		// 发信失败也不回显给调用方，避免通过不同响应推断邮箱是否绑定。
		h.log.Warn("登录验证码邮件发送失败", "err", err)
	}
	writeJSON(w, http.StatusOK, generic)
}

// POST /api/login/email-code/verify
func (h *handlers) loginEmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	if !h.emailCodeLoginEnabled(r.Context()) {
		writeError(w, http.StatusForbidden, "邮箱验证码登录已关闭")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	email := normalizeEmail(mapStr(body, "email", ""))
	code := strings.TrimSpace(mapStr(body, "code", ""))
	if email == "" {
		writeError(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	if len(code) != 6 {
		writeError(w, http.StatusBadRequest, "请输入 6 位数字验证码")
		return
	}
	username, mfaEnabled, _, ok := h.findAdminByVerifiedEmail(r.Context(), email)
	if !ok {
		writeError(w, http.StatusNotFound, "该邮箱未绑定管理员账号或尚未通过验证")
		return
	}
	okCode, status, msg := h.consumeEmailCode(r.Context(), email, code, emailLoginPurpose)
	if !okCode {
		writeError(w, status, msg)
		return
	}
	if mfaEnabled == 1 {
		ticket, err := auth.Sign(auth.Claims{
			Sub: username, Purpose: emailLoginTicketPurpose, Typ: auth.TokenTypeEmailLogin,
			Exp: nowMillis() + 10*60*1000,
		}, h.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "登录失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"requires_mfa": true, "step": "totp_required", "email": email,
			"email_login_token": ticket,
		})
		return
	}
	token, err := auth.IssueAdminTokenTTL(username, h.cfg.JWTSecret, false, h.cfg.AdminTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "requires_mfa": false, "token_ttl_ms": h.cfg.AdminTokenTTL.Milliseconds()})
}

// POST /api/login/email-code/mfa
func (h *handlers) loginEmailCodeMfa(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	ticketRaw := strings.TrimSpace(mapStr(body, "email_login_token", ""))
	totpCode := strings.TrimSpace(mapStr(body, "totp_code", ""))
	backupCode := strings.ToUpper(strings.TrimSpace(mapStr(body, "backup_code", "")))
	if ticketRaw == "" {
		writeError(w, http.StatusUnauthorized, "登录会话已失效，请重新获取验证码")
		return
	}
	ticket, err := auth.Parse(ticketRaw, h.cfg.JWTSecret)
	if err != nil || ticket.Typ != auth.TokenTypeEmailLogin || ticket.Purpose != emailLoginTicketPurpose {
		writeError(w, http.StatusUnauthorized, "登录会话已失效，请重新获取验证码")
		return
	}
	var (
		mfaEnabled int64
		secret     sql.NullString
	)
	err = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(mfa_enabled,0), COALESCE(mfa_secret,'') FROM admin_users WHERE username = ? LIMIT 1`,
		ticket.Sub).Scan(&mfaEnabled, &secret)
	if err != nil || mfaEnabled != 1 {
		writeError(w, http.StatusUnauthorized, "账号状态异常，请重新登录")
		return
	}

	if backupCode != "" {
		var id int64
		err := h.db.QueryRowContext(r.Context(),
			`SELECT id FROM mfa_backup_codes WHERE username = ? AND code = ? AND used = 0 LIMIT 1`,
			ticket.Sub, backupCode).Scan(&id)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "备份码无效")
			return
		}
		_, _ = h.db.ExecContext(r.Context(), `UPDATE mfa_backup_codes SET used = 1, used_at = ? WHERE id = ?`, nowMillis(), id)
		_, _ = h.db.ExecContext(r.Context(), `UPDATE admin_users SET last_backup_login = ? WHERE username = ?`, nowMillis(), ticket.Sub)
		token, err := auth.Sign(auth.Claims{
			Sub: ticket.Sub, Exp: nowMillis() + 24*3600*1000,
			Role: "admin", RequiresMFA: true, MFAVerified: true,
			ViaBackupCode: true, Typ: auth.TokenTypeAccess,
		}, h.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "登录失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "message": "登录成功", "token_ttl_ms": h.cfg.AdminTokenTTL.Milliseconds()})
		return
	}

	if totpCode == "" {
		writeError(w, http.StatusBadRequest, "请输入验证码")
		return
	}
	if len(totpCode) != 6 {
		writeError(w, http.StatusBadRequest, "请输入 6 位动态码")
		return
	}
	if !secret.Valid || secret.String == "" {
		writeError(w, http.StatusBadRequest, "MFA未设置")
		return
	}
	if !auth.VerifyTOTP(secret.String, totpCode, 1) {
		writeError(w, http.StatusUnauthorized, "验证码无效或已过期")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `UPDATE admin_users SET last_mfa_login = ? WHERE username = ?`, nowMillis(), ticket.Sub)
	token, err := auth.IssueAdminTokenTTL(ticket.Sub, h.cfg.JWTSecret, true, h.cfg.AdminTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "requires_mfa": true, "message": "MFA验证成功", "token_ttl_ms": h.cfg.AdminTokenTTL.Milliseconds()})
}
