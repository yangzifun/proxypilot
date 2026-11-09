package api

import (
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// GET /api/shell/local —— 本机（控制服务器）远程终端。
//
// 本机不再占用 servers 表，因此单独提供入口：校验令牌后直接在本机起 PTY。
func (h *handlers) shellLocal(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少登录令牌")
		return
	}
	claims, err := auth.Parse(token, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	if claims.Role == "user" {
		writeError(w, http.StatusForbidden, "用户端令牌不能使用管理终端")
		return
	}
	if !h.wsOriginAllowed(r) {
		writeError(w, http.StatusForbidden, "WebSocket Origin 不被允许")
		return
	}
	ws, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	stopPing := make(chan struct{})
	defer close(stopPing)
	go ws.keepAlive(stopPing)
	_ = ws.WriteText(`{"type":"ready"}`)
	h.serveLocalShell(ws)
}