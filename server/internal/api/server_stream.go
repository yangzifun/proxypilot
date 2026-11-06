package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// sseServersStream 通过 SSE 向管理端推送服务器状态变化信号。
// 客户端收到 signal 后刷新一次快照，避免在 URL 中携带大负载。
func (h *handlers) sseServersStream(w http.ResponseWriter, r *http.Request) {
	// EventSource 无法设置请求头：允许 ?token= 或 Authorization: Bearer
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		if raw := strings.TrimSpace(r.Header.Get("Authorization")); strings.HasPrefix(raw, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
		}
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "未授权")
		return
	}
	claims, err := auth.Parse(token, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "Token 过期")
		return
	}
	if claims.Role == "user" {
		writeError(w, http.StatusForbidden, "用户令牌不能访问管理接口")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "当前连接不支持流式输出")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 3000\n\n")
	flusher.Flush()

	last := ""
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-ticker.C:
			rows, err := queryRowsAsMaps(r.Context(), h.db,
				`SELECT id, COALESCE(last_status,'unknown') AS status,
				        COALESCE(agent_last_seen,0) AS agent_last_seen,
				        COALESCE(last_latency_ms,0) AS last_latency_ms
				 FROM servers ORDER BY id ASC`)
			if err != nil {
				continue
			}
			buf, _ := json.Marshal(rows)
			sig := string(buf)
			if sig == last {
				continue
			}
			last = sig
			fmt.Fprintf(w, "data: %s\n\n", sig)
			flusher.Flush()
		}
	}
}
