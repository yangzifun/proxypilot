package api

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed assets/install.sh
var installScriptBody string

// siteOrigin 返回安装脚本里使用的对外基址。
//
// 安全约束（低-01）：优先使用配置的对外基址；否则从请求推导，且只有可信反代
// 才采信 X-Forwarded-Proto/Host（防止伪造安装命令里的下载/伺服地址）。
func (h *handlers) siteOrigin(r *http.Request) string {
	if base := configuredSiteOrigin(); base != "" {
		return base
	}
	scheme := "https"
	if r.TLS == nil && (strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1")) {
		scheme = "http"
	}
	host := r.Host
	if isTrustedProxyRequest(r) {
		if proto := strings.TrimSpace(firstForwardedValue(r.Header.Get("X-Forwarded-Proto"))); proto != "" {
			scheme = proto
		}
		if fwd := firstForwardedValue(r.Header.Get("X-Forwarded-Host")); fwd != "" {
			host = fwd
		}
	}
	return scheme + "://" + host
}

func (h *handlers) renderInstallScript(r *http.Request) string {
	body := installScriptBody
	body = strings.ReplaceAll(body, "ORIGIN='https://your-worker.example.com'", "ORIGIN='"+h.siteOrigin(r)+"'")
	body = strings.ReplaceAll(body, "ORIGIN=''", "ORIGIN='"+h.siteOrigin(r)+"'")
	if base := h.agentBaseFor(r); base != "" {
		body = strings.ReplaceAll(body, "https://s3.proxypilot.yangzifun.org/agent", strings.TrimRight(base, "/"))
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return body
}

// GET /api/agent/install.sh、/install.sh、/agent/install.sh
func (h *handlers) agentInstallScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.renderInstallScript(r)))
}
