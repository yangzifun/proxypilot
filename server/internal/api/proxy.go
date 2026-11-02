package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// legacyPaths 是可以转发给旧 Worker 的路径前缀（页面仍由 Worker 提供，不在这里转发）。
func legacyPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || p == "/api" ||
		strings.HasPrefix(p, "/manage") ||
		p == "/sub" || strings.HasPrefix(p, "/sub/") ||
		p == "/clash-config" || strings.HasPrefix(p, "/batch-configs/") ||
		p == "/install.sh" || p == "/agent/install.sh"
}

// proxyAttempt 把请求原样转发到 PP_LEGACY_BASE。
// 返回 false 表示未配置上游（调用方应返回 501）。
func (h *handlers) proxyAttempt(w http.ResponseWriter, r *http.Request) bool {
	base := strings.TrimRight(h.cfg.LegacyBase, "/")
	if base == "" {
		return false
	}
	if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false // WebSocket 需要连接劫持，交给专门的网关
	}
	// 防自环：PP_LEGACY_BASE 误配成本站时直接报错，避免无限代理
	if u, err := url.Parse(base); err == nil && strings.EqualFold(u.Host, r.Host) {
		writeError(w, http.StatusBadGateway, "PP_LEGACY_BASE 指向了本站，已拒绝转发（请改为旧 Worker 地址或留空）")
		return true
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	target := base + r.URL.RequestURI()
	req, err := http.NewRequestWithContext(ctx, r.Method, target, r.Body)
	if err != nil {
		writeError(w, http.StatusBadGateway, "转发请求失败")
		return true
	}
	copyHeaders(req.Header, r.Header, map[string]bool{
		"Host": true, "Connection": true, "Content-Length": true,
		"Accept-Encoding": true, "Upgrade": true,
	})
	req.ContentLength = r.ContentLength
	if ip := clientIP(r); ip != "" {
		req.Header.Set("X-Forwarded-For", ip)
	}

	resp, err := h.legacy.Do(req)
	if err != nil {
		h.log.Error("转发到旧 Worker 失败", "url", target, "err", err)
		writeError(w, http.StatusBadGateway, "旧服务暂不可用："+err.Error())
		return true
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		if hopByHop(k) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return true
}

func hopByHop(k string) bool {
	switch strings.ToLower(k) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
		"te", "trailers", "transfer-encoding", "upgrade", "content-length":
		return true
	}
	return false
}

func copyHeaders(dst, src http.Header, skip map[string]bool) {
	for k, vs := range src {
		if skip[k] {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

func clientIP(r *http.Request) string {
	for _, h := range []string{"CF-Connecting-IP", "True-Client-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			return v
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	return ""
}
