package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// POST /api/change-password  body: {"oldPassword": "...", "newPassword": "..."}
// 字段名沿用 Worker 的 camelCase，避免前端改动。
func (h *handlers) changePassword(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未授权")
		return
	}
	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if strings.TrimSpace(req.NewPassword) == "" {
		writeError(w, http.StatusBadRequest, "新密码不能为空")
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}

	var id int64
	var hash string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, password_hash FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusUnauthorized, "账号不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if ok, _ := auth.VerifyPassword(hash, req.OldPassword); !ok {
		writeError(w, http.StatusForbidden, "旧密码错误")
		return
	}
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE admin_users SET password_hash = ? WHERE id = ?`, newHash, id); err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /api/mfa/status
func (h *handlers) mfaStatus(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsFrom(r)
	var (
		enabled    int64
		lastMFA    int64
		lastBackup int64
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(mfa_enabled,0), COALESCE(last_mfa_login,0), COALESCE(last_backup_login,0)
		   FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).
		Scan(&enabled, &lastMFA, &lastBackup)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "账号不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	var remaining int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM mfa_backup_codes WHERE username = ? AND used = 0`, claims.Sub).Scan(&remaining)
	writeJSON(w, http.StatusOK, map[string]any{
		"mfa_enabled":            enabled == 1,
		"last_mfa_login":         lastMFA,
		"backup_codes_remaining": remaining,
	})
}
