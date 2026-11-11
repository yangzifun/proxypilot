package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

type ctxKey int

const claimsKey ctxKey = 0

type authOpts struct {
	// superOnly 仅超级管理员可访问。
	superOnly bool
	// allowUser 允许 role=user 的用户端令牌访问（默认拒绝，与 Worker 一致）。
	allowUser bool
}

// authed 校验 Bearer JWT 并按 opts 做角色/超管判定。
func (h *handlers) authed(opts authOpts, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {

		raw := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(raw, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "未授权")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		claims, err := auth.Parse(token, h.cfg.JWTSecret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Token过期")
			return
		}
		if claims.Role == "user" && !opts.allowUser {
			writeError(w, http.StatusForbidden, "用户端登录令牌不能访问管理接口")
			return
		}
		if opts.superOnly && claims.Sub != h.cfg.SuperAdmin {
			writeError(w, http.StatusForbidden, "仅超级管理员可访问")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), claimsKey, claims)))
	}
}

func claimsFrom(r *http.Request) (auth.Claims, bool) {
	c, ok := r.Context().Value(claimsKey).(auth.Claims)
	return c, ok
}
