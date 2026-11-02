package api

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed assets/install.sh
var installScriptBody string

func (h *handlers) siteOrigin(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && strings.HasPrefix(r.Host, "localhost") {
		scheme = "http"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	}
	host := r.Host
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		host = fwd
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
