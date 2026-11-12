package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

type loginRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	TOTPCode  string `json:"totp_code"`
	Step      string `json:"step"`
	Turnstile string `json:"cf_turnstile_response"`
	MFAToken  string `json:"mfa_token"`
}

// login 对应 POST /api/login：账号密码 + 可选 TOTP。
//
// 安全约束：
//   - 第一步（密码）必须携带有效 Turnstile；
//   - 第二步（verify_totp）必须携带第一步签发的 5 分钟 mfa_token，
//     不再重复校验密码、也不允许用 step 参数绕过 Turnstile；
//   - 两个步骤共用登录失败限速，避免无限爆破密码 / TOTP。
func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	if !h.passwordLoginEnabled(r.Context()) {
		writeError(w, http.StatusForbidden, "密码登录已禁用，请使用单点登录")
		return
	}
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "用户名或密码不能为空")
		return
	}

	key := loginRateKey(r, req.Username)
	if wait, blocked := loginLimiter.Blocked(key); blocked {
		if wait == loginBlockFor { // 本轮刚触发锁定，推送一次告警
			h.alertLoginLockout(key)
		}
		w.Header().Set("Retry-After", retryAfterSeconds(wait))
		writeError(w, http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}

	if req.Step == "verify_totp" {
		h.loginVerifyMFA(w, r, req, key)
		return
	}

	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "用户名或密码不能为空")
		return
	}
	if err := h.verifyTurnstile(r, req.Turnstile); err != nil {
		loginLimiter.Fail(key)
		writeError(w, http.StatusForbidden, err.Error())
		return
	}

	var (
		id         int64
		username   string
		hash       string
		mfaEnabled int64
		mfaSecret  sql.NullString
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, username, password_hash, COALESCE(mfa_enabled,0), mfa_secret
		   FROM admin_users WHERE username = ? LIMIT 1`, req.Username).
		Scan(&id, &username, &hash, &mfaEnabled, &mfaSecret)
	if errors.Is(err, sql.ErrNoRows) {
		// 与存在账号时耗时接近，降低用户名枚举
		_, _ = auth.VerifyPassword(
			"5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
			req.Password)
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if err != nil {
		h.log.Error("查询管理员失败", "err", err)
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	ok, needsRehash := auth.VerifyPassword(hash, req.Password)
	if !ok {
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	// 登录成功即把遗留 SHA-256 哈希升级为 PBKDF2
	if needsRehash {
		if newHash, err := auth.HashPassword(req.Password); err == nil {
			if _, err := h.db.ExecContext(r.Context(),
				`UPDATE admin_users SET password_hash = ? WHERE id = ?`, newHash, id); err != nil {
				h.log.Warn("升级口令哈希失败", "user", username, "err", err)
			}
		}
	}

	if mfaEnabled == 1 {
		mfaToken, err := auth.IssueMFAToken(username, h.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "签发 MFA 挑战失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"requires_mfa": true,
			"mfa_token":    mfaToken,
			"username":     username,
			"step":         "totp_required",
		})
		return
	}

	loginLimiter.Reset(key)
	token, err := auth.IssueAdminTokenTTL(username, h.cfg.JWTSecret, false, h.cfg.AdminTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "requires_mfa": false, "token_ttl_ms": h.cfg.AdminTokenTTL.Milliseconds()})
}

// loginVerifyMFA 只接受 password+Turnstile 通过后签发的 mfa_token。
func (h *handlers) loginVerifyMFA(w http.ResponseWriter, r *http.Request, req loginRequest, key string) {
	claims, err := auth.Parse(strings.TrimSpace(req.MFAToken), h.cfg.JWTSecret)
	if err != nil || claims.Typ != auth.TokenTypeMFA || claims.Purpose != "mfa" || claims.Sub == "" || claims.Sub != req.Username {
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "验证码无效或已过期")
		return
	}

	var (
		id         int64
		mfaEnabled int64
		mfaSecret  sql.NullString
	)
	err = h.db.QueryRowContext(r.Context(),
		`SELECT id, COALESCE(mfa_enabled,0), mfa_secret FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).
		Scan(&id, &mfaEnabled, &mfaSecret)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "验证码无效或已过期")
		return
	}
	if mfaEnabled != 1 || !mfaSecret.Valid || !auth.VerifyTOTP(mfaSecret.String, req.TOTPCode, 1) {
		loginLimiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "验证码无效或已过期")
		return
	}

	loginLimiter.Reset(key)
	now := nowMillis()
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE admin_users SET last_mfa_login = ? WHERE id = ?`, now, id); err != nil {
		h.log.Warn("记录 MFA 登录失败", "user", claims.Sub, "err", err)
	}
	token, err := auth.IssueAdminTokenTTL(claims.Sub, h.cfg.JWTSecret, true, h.cfg.AdminTokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "requires_mfa": true, "message": "MFA验证成功", "token_ttl_ms": h.cfg.AdminTokenTTL.Milliseconds()})
}
