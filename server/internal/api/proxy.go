package api

import (
	"net"
	"net/http"
	"strings"
)

// clientIP 返回请求来源 IP。
//
// 安全约束（低-01）：只有来自可信反代（Cloudflare/回环，可配 PP_TRUSTED_PROXIES）
// 的请求才采信转发头；直连请求一律使用 TCP 对端地址，防止伪造 X-Forwarded-For
// 绕过登录限速或污染审计。
func clientIP(r *http.Request) string {
	if isTrustedProxyRequest(r) {
		for _, h := range []string{"CF-Connecting-IP", "True-Client-IP", "X-Real-IP"} {
			if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
				if ip := net.ParseIP(v); ip != nil {
					return ip.String()
				}
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if ip := net.ParseIP(firstForwardedValue(xff)); ip != nil {
				return ip.String()
			}
		}
	}
	if ip := remoteAddrIP(r); ip != nil {
		return ip.String()
	}
	return ""
}
