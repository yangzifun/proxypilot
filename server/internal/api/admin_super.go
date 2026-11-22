package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// isSuperAdmin 统一判断管理员是否超级管理员：
//  1. PP_SUPER_ADMIN（root）永远视为超管，不依赖 DB 行；
//  2. 其它账号看 admin_users.is_super=1。
//
// 调用方必须已经通过 authed 校验；ctx 用于查询 DB。DB 不存在/查询失败时 fail-closed。
func (h *handlers) isSuperAdmin(ctx context.Context, username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if root := strings.TrimSpace(h.cfg.SuperAdmin); root != "" && username == root {
		return true
	}
	if h.db == nil {
		return false
	}
	var flag int64
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(is_super,0) FROM admin_users WHERE username = ? LIMIT 1`, username).Scan(&flag)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && h.log != nil {
			h.log.Warn("查询管理员超管标记失败（按普通管理员处理）", "username", username, "err", err)
		}
		return false
	}
	return flag == 1
}

// GET /api/auth/me（authed）：返回当前登录账号身份，供前端刷新后判断权限。
func (h *handlers) authMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r)
	if !ok || strings.TrimSpace(claims.Sub) == "" {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	root := strings.TrimSpace(h.cfg.SuperAdmin) != "" && claims.Sub == h.cfg.SuperAdmin
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"username": claims.Sub,
		"is_super": h.isSuperAdmin(r.Context(), claims.Sub),
		"is_root":  root,
	})
}