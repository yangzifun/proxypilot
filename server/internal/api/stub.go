package api

import "net/http"

// notFound 是 ServeMux 的兜底：未注册路径统一返回 JSON 404。
//
// V1.5.2 起旧 Worker 兜底（PP_LEGACY_BASE / 501 转发）已移除，未知 API
// 不再被代理或返回 501，便于调用方区分“接口不存在”和“服务异常”。
func (h *handlers) notFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{
		"error": "Not Found",
		"path":  r.URL.Path,
	})
}
