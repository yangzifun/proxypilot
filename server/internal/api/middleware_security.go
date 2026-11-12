package api

import "net/http"

// cspPolicy 强制模式 CSP（不再 Report-Only，也不再依赖每请求 nonce）。
//
// 为什么不使用 nonce（2026-11-12 白屏事故的根因）：
// nonce 要求「CSP 响应头与 HTML body 来自同一次响应」。本部署前面有 Cloudflare 与
// 各级缓存，实测出现过 body 里的 nonce 与响应头 nonce 不一致；强制 CSP 下浏览器
// 以头里的 nonce 为准，于是 HTML 里所有带 nonce 的脚本全部被拦，后台白屏。
//
// 现在改为：
//   - CSP 内容是常量，对所有请求完全相同（无 nonce/无 Host 等每请求变化的值）；
//   - HTML 里没有任何可执行内联脚本：动态数据走 <script type="application/json"> 数据块，
//     可执行脚本全部是同源外部文件（见 page_scripts.go）；
// 这样无论缓存/代理如何拆分响应，CSP 与 HTML 都不会再互相矛盾。
//
// 指令说明：
//   - script-src 只允许同源脚本与明确列出的 Cloudflare 脚本源，无 'unsafe-inline'；
//   - connect-src 允许同源 WebSocket（管理端终端）。这里用 ws:/wss: 方案级白名单而不是
//     具体 Host：Host 是每请求变化的值，写进 CSP 会重新引入「头与缓存版本不一致」的问题；
//   - style-src 保留 'unsafe-inline'（内联样式不构成脚本执行面）；
//   - report-uri 保留，强制模式下违规仍上报到 /api/csp-report 便于线上观察。
const cspPolicy = "default-src 'self'; " +
	"script-src 'self' https://static.cloudflareinsights.com https://challenges.cloudflare.com; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: https://s3.yangzifun.org; " +
	"font-src 'self' data:; connect-src 'self' ws: wss: https://cloudflareinsights.com https://challenges.cloudflare.com; " +
	"frame-src https://challenges.cloudflare.com; frame-ancestors 'self'; base-uri 'self'; " +
	"form-action 'self'; object-src 'none'; report-uri /api/csp-report"

// secureHeaders 生产环境基础安全响应头 + 强制 CSP。
func (h *handlers) secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("X-Frame-Options", "SAMEORIGIN")
		hdr.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		hdr.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// 常量策略：同一份 CSP 对 HTML/资源/API 都一致，无每请求变量。
		hdr.Set("Content-Security-Policy", cspPolicy)
		next.ServeHTTP(w, r)
	})
}