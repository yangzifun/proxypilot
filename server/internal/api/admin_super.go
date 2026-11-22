package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"strings"
)

// isRootAdmin 判断账号是否是「根超管」：
//  1. 优先 DB `admin_users.is_root=1`（改名后仍保持 root 身份）；
//  2. 只有 DB 中完全没有 is_root 行（未迁移/首次启动）时，才回退
//     username == PP_SUPER_ADMIN，兼容旧部署与启动前的状态。
//
// 调用方必须已通过 authed 校验。DB 不存在/查询失败时回退旧 cfg 用户名（保证首次启动/故障时 root 仍可进入）。
func (h *handlers) isRootAdmin(ctx context.Context, username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	rootUsername := strings.TrimSpace(h.cfg.SuperAdmin)
	if h.db == nil {
		return rootUsername != "" && username == rootUsername
	}
	var flag, rootRows int64
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE((SELECT is_root FROM admin_users WHERE username = ? LIMIT 1), 0),
		        (SELECT COUNT(*) FROM admin_users WHERE is_root = 1)`, username).
		Scan(&flag, &rootRows)
	if err != nil {
		if h.log != nil {
			h.log.Warn("查询根超管标记失败（回退 PP_SUPER_ADMIN 兼容）", "username", username, "err", err)
		}
		return rootUsername != "" && username == rootUsername
	}
	if flag == 1 {
		return true
	}
	if rootRows == 0 {
		// 旧库/首次启动：没有任何 is_root 行时，PP_SUPER_ADMIN 仍视为 root。
		return rootUsername != "" && username == rootUsername
	}
	// 已有 root 行：旧 cfg 用户名不再自动拥有 root（改名后身份跟随 DB）。
	return false
}

// isSuperAdmin 统一判断管理员是否超级管理员：
//  1. isRootAdmin（DB is_root 或首次启动的 PP_SUPER_ADMIN 兼容）；
//  2. DB admin_users.is_super=1。
func (h *handlers) isSuperAdmin(ctx context.Context, username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	if h.isRootAdmin(ctx, username) {
		return true
	}
	if h.db == nil {
		return false
	}
	var flag int64
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(is_super,0) FROM admin_users WHERE username = ? LIMIT 1`, username).Scan(&flag)
	if err != nil {
		if err != sql.ErrNoRows && h.log != nil {
			h.log.Warn("查询管理员超管标记失败（按普通管理员处理）", "username", username, "err", err)
		}
		return false
	}
	return flag == 1
}

// EnsureRootAdmin 启动时兜底：DB 没有任何 is_root 行时，把 PP_SUPER_ADMIN
// 对应账号补成 is_root=1、is_super=1（首次启动/旧库迁移后）。已有 root 行则不动。
func EnsureRootAdmin(ctx context.Context, pool *sql.DB, username string, log *slog.Logger) {
	username = strings.TrimSpace(username)
	if pool == nil || username == "" {
		return
	}
	var rootRows int64
	if err := pool.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM admin_users WHERE is_root = 1`).Scan(&rootRows); err != nil {
		if log != nil {
			log.Warn("检查根超管标记失败（跳过启动兜底）", "err", err)
		}
		return
	}
	if rootRows > 0 {
		return
	}
	res, err := pool.ExecContext(ctx,
		`UPDATE admin_users SET is_root = 1, is_super = 1 WHERE username = ?`, username)
	if err != nil {
		if log != nil {
			log.Warn("补充根超管标记失败", "username", username, "err", err)
		}
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if log != nil {
			log.Warn("未找到 PP_SUPER_ADMIN 对应管理员，无法补充根超管标记", "username", username)
		}
		return
	}
	if log != nil {
		log.Info("已补充根超管标记", "username", username)
	}
}

// GET /api/auth/me（authed）：返回当前登录账号身份，供前端刷新后判断权限。
func (h *handlers) authMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := claimsFrom(r)
	if !ok || strings.TrimSpace(claims.Sub) == "" {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"username": claims.Sub,
		"is_super": h.isSuperAdmin(r.Context(), claims.Sub),
		"is_root":  h.isRootAdmin(r.Context(), claims.Sub),
	})
}
