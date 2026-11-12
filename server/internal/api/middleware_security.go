package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"regexp"
)

// cspNonceCtxKey 把本次请求的 CSP nonce 传给 HTML 渲染函数（server-rendered 页面）。
type cspNonceCtxKey struct{}

// cspNonce 返回当前请求的 CSP nonce；未经过 secureHeaders（如单测直接调用 handler）时为空。
func cspNonce(r *http.Request) string {
	if r == nil {
		return ""
	}
	v, _ := r.Context().Value(cspNonceCtxKey{}).(string)
	return v
}

// newCSPNonce 生成 128 位随机 nonce（base64url，符合 CSP nonce 字符集）。
func newCSPNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时宁可不放行内联脚本（页面降级），也不退化成可预测 nonce。
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var cspHostRe = regexp.MustCompile(`^[A-Za-z0-9._:\[\]-]{1,255}$`)

// cspPolicyWithNonce 构造强制模式 CSP（不再 Report-Only）。
//
// 关键点：
//   - script-src 只允许同源、nonce 以及明确列出的 Cloudflare 脚本源，未带 nonce 的
//     内联脚本一律拦截（反射 XSS 无法执行）；
//   - 管理端 /api/shell/* 使用同主机 WebSocket，connect-src 显式放行 ws(s)://<Host>；
//   - style-src 保留 'unsafe-inline'（页面内联样式与 antd 动态样式依赖它，不构成脚本执行）。
func cspPolicyWithNonce(nonce, host string) string {
	connectWS := ""
	if cspHostRe.MatchString(host) {
		connectWS = " ws://" + host + " wss://" + host
	}
	scriptNonce := ""
	if nonce != "" {
		scriptNonce = " 'nonce-" + nonce + "'"
	}
	return "default-src 'self'; " +
		"script-src 'self'" + scriptNonce + " https://static.cloudflareinsights.com https://challenges.cloudflare.com; " +
		"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https://s3.yangzifun.org; " +
		"font-src 'self' data:; connect-src 'self'" + connectWS + " https://cloudflareinsights.com https://challenges.cloudflare.com; " +
		"frame-src https://challenges.cloudflare.com; frame-ancestors 'self'; base-uri 'self'; " +
		"form-action 'self'; object-src 'none'; report-uri /api/csp-report"
}

// secureHeaders 生产环境基础安全响应头 + 强制 CSP。
func (h *handlers) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("X-Frame-Options", "SAMEORIGIN")
		hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hdr.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		nonce := newCSPNonce()
		// 从 Report-Only 切换为强制模式：CSP 只写 Content-Security-Policy，不再写
		// Content-Security-Policy-Report-Only（否则浏览器只报告不拦截）。
		hdr.Set("Content-Security-Policy", cspPolicyWithNonce(nonce, r.Host))
		ctx := context.WithValue(r.Context(), cspNonceCtxKey{}, nonce)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
