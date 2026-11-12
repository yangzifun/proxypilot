package api

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

const mfaBackupCodeCount = 10

func generateBackupCodes(n int) ([]string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]string, 0, n)
	buf := make([]byte, 8)
	for i := 0; i < n; i++ {
		if _, err := rand.Read(buf); err != nil {
			return nil, err
		}
		b := make([]byte, 8)
		for j, v := range buf {
			b[j] = alphabet[int(v)%len(alphabet)]
		}
		out = append(out, string(b))
	}
	return out, nil
}

func (h *handlers) currentUserRow(w http.ResponseWriter, r *http.Request) (auth.Claims, string, bool) {
	claims, ok := claimsFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未授权")
		return claims, "", false
	}
	var hash string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT password_hash FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "账号不存在")
		return claims, "", false
	}
	return claims, hash, true
}

func (h *handlers) verifyCurrentPassword(r *http.Request, storedHash, password string) bool {
	ok, _ := auth.VerifyPassword(storedHash, password)
	return ok
}

// POST /api/mfa/init  body: {"password": "..."}
func (h *handlers) mfaInit(w http.ResponseWriter, r *http.Request) {
	claims, hash, ok := h.currentUserRow(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if !h.verifyCurrentPassword(r, hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	var enabled int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(mfa_enabled,0) FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).Scan(&enabled)
	if enabled == 1 {
		writeError(w, http.StatusBadRequest, "MFA已启用")
		return
	}
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	otpauth := auth.OTPAuthURL(claims.Sub, secret, "YZFN Admin")
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     true,
		"secret":      secret,
		"otpauth_url": otpauth,
		"qr_url":      "https://qrcode.api.yangzifun.org/qr?text=" + url.QueryEscape(otpauth) + "&cellSize=4&margin=1",
	})
}

// POST /api/mfa/verify-first  body: {"totp_code": "...", "secret": "..."}
func (h *handlers) mfaVerifyFirst(w http.ResponseWriter, r *http.Request) {
	claims, _, ok := h.currentUserRow(w, r)
	if !ok {
		return
	}
	var req struct {
		TOTPCode string `json:"totp_code"`
		Secret   string `json:"secret"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if !auth.VerifyTOTP(req.Secret, strings.TrimSpace(req.TOTPCode), 1) {
		writeError(w, http.StatusBadRequest, "验证码无效")
		return
	}
	codes, err := generateBackupCodes(mfaBackupCodeCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE admin_users SET mfa_enabled = 1, mfa_secret = ? WHERE username = ?`,
		req.Secret, claims.Sub); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	now := nowMillis()
	for _, code := range codes {
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)`,
			claims.Sub, code, now); err != nil {
			writeError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "backup_codes": codes})
}

// POST /api/mfa/disable  body: {"password": "..."}
func (h *handlers) mfaDisable(w http.ResponseWriter, r *http.Request) {
	claims, hash, ok := h.currentUserRow(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if !h.verifyCurrentPassword(r, hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE admin_users SET mfa_enabled = 0, mfa_secret = NULL WHERE username = ?`, claims.Sub); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM mfa_backup_codes WHERE username = ?`, claims.Sub); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// POST /api/mfa/backup-codes/regenerate  body: {"password": "..."}
func (h *handlers) mfaRegenerateBackupCodes(w http.ResponseWriter, r *http.Request) {
	claims, hash, ok := h.currentUserRow(w, r)
	if !ok {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if !h.verifyCurrentPassword(r, hash, req.Password) {
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	codes, err := generateBackupCodes(mfaBackupCodeCount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`DELETE FROM mfa_backup_codes WHERE username = ?`, claims.Sub); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	now := nowMillis()
	for _, code := range codes {
		if _, err := tx.ExecContext(r.Context(),
			`INSERT INTO mfa_backup_codes (username, code, created_at, used) VALUES (?, ?, ?, 0)`,
			claims.Sub, code, now); err != nil {
			writeError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "backup_codes": codes})
}

// POST /api/mfa/login-with-backup（公开） body: {"username","password","backup_code"}
func (h *handlers) mfaLoginWithBackup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MFAToken   string `json:"mfa_token"`
		BackupCode string `json:"backup_code"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.BackupCode))
	if code == "" {
		writeError(w, http.StatusBadRequest, "备份码不能为空")
		return
	}
	claims, err := auth.Parse(strings.TrimSpace(req.MFAToken), h.cfg.JWTSecret)
	if err != nil || claims.Typ != auth.TokenTypeMFA || claims.Purpose != "mfa" || claims.Sub == "" {
		writeError(w, http.StatusUnauthorized, "验证信息已过期，请重新登录")
		return
	}
	username := claims.Sub
	key := loginRateKey(r, username)
	if wait, blocked := loginLimiter.Blocked(key); blocked {
		w.Header().Set("Retry-After", retryAfterSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}

	var codeID int64
	err = h.db.QueryRowContext(r.Context(),
		`SELECT c.id FROM mfa_backup_codes c JOIN admin_users u ON u.username = c.username
		  WHERE c.username = ? AND u.mfa_enabled = 1 AND c.code = ? AND c.used = 0 LIMIT 1`,
		username, code).Scan(&codeID)
	if errors.Is(err, sql.ErrNoRows) {
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "备份码无效")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE mfa_backup_codes SET used = 1, used_at = ? WHERE id = ?`, nowMillis(), codeID); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	loginLimiter.Reset(key)
	token, err := auth.Sign(auth.Claims{
		Sub: username, Exp: nowMillis() + 86_400_000,
		RequiresMFA: true, MFAVerified: true, ViaBackupCode: true,
		Typ: auth.TokenTypeAccess,
	}, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "message": "登录成功"})
}
