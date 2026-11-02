package api

import "net/http"

// notMigrated 统一处理尚未迁移的路径：
//  1. 配置了 PP_LEGACY_BASE → 原样转发给旧 Worker（保证功能不中断）
//  2. 否则：/api/*、/manage*、订阅类路径 → 501；其他 → 404
func (h *handlers) notMigrated(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if legacyPath(p) && h.proxyAttempt(w, r) {
		return
	}
	if legacyPath(p) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error":  "该接口尚未迁移到 Go 服务，且未配置 PP_LEGACY_BASE 转发",
			"method": r.Method,
			"path":   p,
		})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "Not Found",
		"path":  p,
	})
}
