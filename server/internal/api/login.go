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
}

// login 对应 POST /api/login：账号密码 + 可选 TOTP。
// 与 Worker 的行为差异：未配置 Turnstile secret 时直接放行（Worker 的旧实现要求非空 token）。
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
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "用户名或密码不能为空")
		return
	}
	if req.Step != "verify_totp" {
		if err := h.verifyTurnstile(r, req.Turnstile); err != nil {
			writeError(w, http.StatusForbidden, err.Error())
			return
		}
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

	// step=verify_totp 会跳过 Turnstile，因此必须只对“确实启用了 MFA”的账号生效；
	// 否则未启用 MFA 的账号可以用该 step 绕过人机验证直接拿 token。
	if req.Step == "verify_totp" && mfaEnabled != 1 {
		writeError(w, http.StatusBadRequest, "无效的登录步骤")
		return
	}

	if mfaEnabled == 1 {
		if req.Step != "verify_totp" {
			writeJSON(w, http.StatusOK, map[string]any{
				"requires_mfa": true,
				"username":     username,
				"step":         "totp_required",
			})
			return
		}
		if !mfaSecret.Valid || !auth.VerifyTOTP(mfaSecret.String, req.TOTPCode, 1) {
			writeError(w, http.StatusUnauthorized, "验证码无效或已过期")
			return
		}
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE admin_users SET last_mfa_login = ? WHERE id = ?`, nowMillis(), id); err != nil {
			h.log.Warn("记录 MFA 登录失败", "user", username, "err", err)
		}
	}

	token, err := auth.IssueAdminToken(username, h.cfg.JWTSecret, mfaEnabled == 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发 token 失败")
		return
	}
	resp := map[string]any{"token": token, "requires_mfa": mfaEnabled == 1}
	if mfaEnabled == 1 {
		resp["message"] = "MFA验证成功"
	}
	writeJSON(w, http.StatusOK, resp)
}
