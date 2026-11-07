package api

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed assets_web/app.js
var embeddedAppJS []byte

//go:embed assets_web/app.js.br
var embeddedAppJSBr []byte

//go:embed assets_web/app.js.gz
var embeddedAppJSGz []byte

//go:embed assets_web/app.css
var embeddedAppCSS []byte

//go:embed assets_web/app.css.br
var embeddedAppCSSBr []byte

//go:embed assets_web/app.css.gz
var embeddedAppCSSGz []byte

//go:embed assets_web/xterm-app.js
var embeddedXtermJS []byte

//go:embed assets_web/xterm-app.js.br
var embeddedXtermJSBr []byte

//go:embed assets_web/xterm-app.js.gz
var embeddedXtermJSGz []byte

//go:embed assets_web/meta.json
var embeddedAppMeta []byte

//go:embed assets_web/world.svg
var embeddedWorldSVG []byte

//go:embed assets_web/favicon.svg
var embeddedFaviconSVG []byte

type webMeta struct {
	Version string `json:"version"`
	BuiltAt string `json:"builtAt"`
}

func (h *handlers) webMetaInfo() webMeta {
	var m webMeta
	_ = json.Unmarshal(embeddedAppMeta, &m)
	return m
}

func (h *handlers) assetVersion() string {
	m := h.webMetaInfo()
	sum := sha256.Sum256([]byte(m.Version + "|" + m.BuiltAt))
	return hex.EncodeToString(sum[:4])
}

func (h *handlers) bootJSON(mode string) string {
	meta := h.webMetaInfo()
	turnstile := h.turnstileSiteKey(context.Background())
	var boot map[string]any
	if mode == "portal" {
		boot = map[string]any{
			"mode": "portal", "version": meta.Version, "builtAt": meta.BuiltAt,
			"turnstile": turnstile,
		}
	} else {
		boot = map[string]any{
			"mode": "admin", "superAdmin": h.cfg.SuperAdmin,
			"version": meta.Version, "builtAt": meta.BuiltAt, "flavor": "antd",
			"turnstile":         turnstile,
			"turnstileRequired": strings.TrimSpace(h.cfg.TurnstileSecret) != "",
			"emailLogin":        h.emailCodeLoginEnabled(context.Background()) && h.effectiveMail(context.Background()).Ready(),
			"oidc": map[string]any{
				"enabled":          h.effectiveOIDC(context.Background()).Enabled,
				"buttonText":       firstNonEmpty(h.effectiveOIDC(context.Background()).ButtonText, "使用单点登录（OIDC）"),
				"passwordDisabled": !h.passwordLoginEnabled(context.Background()),
			},
		}
	}
	boot["assetVersion"] = h.assetVersion()
	boot["assetVersion"] = h.assetVersion()
	raw, _ := json.Marshal(boot)
	return string(raw)
}

func (h *handlers) shellHTML(mode string) string {
	meta := h.webMetaInfo()
	isPortal := mode == "portal"
	title := "ProxyPilot 控制台"
	theme := "#0f172a"
	if isPortal {
		title = "ProxyPilot"
		theme = "#f6f7fb"
	}
	ver := h.assetVersion()
	return `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover">
<meta name="theme-color" content="` + theme + `">
<meta name="robots" content="noindex,nofollow">
<title>` + title + `</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg?v=` + ver + `">
<link rel="shortcut icon" href="/favicon.ico?v=` + ver + `">
<link rel="apple-touch-icon" href="/favicon.svg?v=` + ver + `">
<link rel="preload" as="script" href="/admin-app.js?v=` + ver + `" fetchpriority="high">
<link rel="preload" as="style" href="/admin-app.css?v=` + ver + `">
<link rel="stylesheet" href="/admin-app.css?v=` + ver + `">
</head>
<body>
<div id="root">
  <style>
    .pp-boot{min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:14px;background:radial-gradient(circle at 50% 30%,#eef2ff 0,#f8fafc 46%,#f1f5f9 100%);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,"PingFang SC","Microsoft YaHei",sans-serif;color:#0f172a}
    .pp-boot-mark{position:relative;width:76px;height:76px;display:flex;align-items:center;justify-content:center;border-radius:22px;background:linear-gradient(135deg,#6366f1,#06b6d4);box-shadow:0 16px 40px rgba(79,70,229,.28);animation:pp-boot-float 2.4s ease-in-out infinite}
    .pp-boot-mark svg{width:46px;height:46px;filter:drop-shadow(0 2px 4px rgba(15,23,42,.18))}
    .pp-boot-ring{position:absolute;inset:-7px;border-radius:27px;border:2px solid rgba(99,102,241,.18);border-top-color:#6366f1;animation:pp-boot-spin 1.2s linear infinite}
    .pp-boot-title{font-size:20px;font-weight:750;letter-spacing:.2px;margin-top:4px}
    .pp-boot-version{font-size:12px;color:#94a3b8;letter-spacing:.12em}
    .pp-boot-track{width:180px;height:4px;border-radius:999px;background:#e2e8f0;overflow:hidden;margin-top:6px}
    .pp-boot-track i{display:block;width:42%;height:100%;border-radius:999px;background:linear-gradient(90deg,#6366f1,#06b6d4,#6366f1);animation:pp-boot-bar 1.15s ease-in-out infinite}
    .pp-boot-tip{font-size:13px;color:#64748b}
    .pp-boot-retry{font-size:12px;color:#b45309;background:#fffbeb;border:1px solid #fde68a;border-radius:999px;padding:6px 12px}
    .pp-boot-retry a{color:#4f46e5;font-weight:650;text-decoration:none}
    @keyframes pp-boot-float{0%,100%{transform:translateY(0)}50%{transform:translateY(-5px)}}
    @keyframes pp-boot-spin{to{transform:rotate(360deg)}}
    @keyframes pp-boot-bar{0%{transform:translateX(-130%)}100%{transform:translateX(330%)}}
    @media (prefers-reduced-motion:reduce){.pp-boot-mark,.pp-boot-ring,.pp-boot-track i{animation:none}}
  </style>
  <div class="pp-boot" role="status" aria-live="polite">
    <div class="pp-boot-mark">
      <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64" aria-hidden="true"><path d="M12 35.5 52.5 15.5c1.3-.6 2.5.6 2 1.9L38 52.2c-.5 1.3-2.2 1.4-2.8.2l-5.9-12.6-12.2-1.5c-1.5-.2-1.9-2.1-.6-2.8Z" fill="#fff"/><path d="m30.1 39.4 4.1 8.6 4.1-10.2-8.2 1.6Z" fill="#c7d2fe"/></svg>
      <span class="pp-boot-ring"></span>
    </div>
    <div class="pp-boot-title">` + title + `</div>
    <div class="pp-boot-version">` + meta.Version + `</div>
    <div class="pp-boot-track"><i></i></div>
    <div class="pp-boot-tip">正在加载控制台…</div>
    <div class="pp-boot-retry" id="pp-boot-retry" hidden>加载时间较长，<a href="" onclick="location.reload();return false;">刷新页面</a></div>
  </div>
</div>
<script data-cfasync="false">window.__PP_BOOT__=` + h.bootJSON(mode) + `;</script>
<script>
(function () {
  var retry = document.getElementById('pp-boot-retry');
  function showRetry(message) {
    if (!retry || !document.querySelector('.pp-boot')) return;
    if (message) retry.innerHTML = message + '，<a href="" onclick="location.reload();return false;">刷新页面</a>';
    retry.hidden = false;
  }
  window.setTimeout(function () { showRetry('加载时间较长'); }, 12000);
  window.addEventListener('error', function () { showRetry('资源加载失败'); }, true);
})();
</script>
<script data-cfasync="false" src="/admin-app.js?v=` + ver + `" defer fetchpriority="high"></script>
<noscript><p style="font-family:system-ui;padding:24px">当前界面需要启用 JavaScript。</p></noscript>
</body>
</html>`
}

var bootScriptRe = regexp.MustCompile(`window\.__PP_BOOT__\s*=\s*[^;]+;`)

func (h *handlers) serveCustomIndex(w http.ResponseWriter, r *http.Request, mode string) bool {
	dist := strings.TrimSpace(h.cfg.WebDist)
	if dist == "" {
		return false
	}
	index := filepath.Join(dist, "index.html")
	raw, err := os.ReadFile(index)
	if err != nil {
		return false
	}
	boot := `<script data-cfasync="false">window.__PP_BOOT__=` + h.bootJSON(mode) + `;</script>`
	html := string(raw)
	if bootScriptRe.MatchString(html) {
		html = bootScriptRe.ReplaceAllString(html, boot)
	} else {
		html = strings.Replace(html, "</head>", boot+"\n</head>", 1)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(html))
	return true
}

// GET /（Portal）与 /admin、/ui（后台）
func (h *handlers) pageIndex(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch path {
	case "/login", "/login/":
		http.Redirect(w, r, "/admin", http.StatusFound)
		return
	case "/manage", "/manage/":
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	mode := "admin"
	if path == "/" || path == "/me" || path == "/me/" {
		mode = "portal"
	}
	if h.serveCustomIndex(w, r, mode) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 外壳不含用户数据，按路径缓存 30 秒，避免每次打开都回源。
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	ver := h.assetVersion()
	w.Header().Set("Link", "</admin-app.js?v="+ver+">; rel=preload; as=script, </admin-app.css?v="+ver+">; rel=preload; as=style")
	_, _ = w.Write([]byte(h.shellHTML(mode)))
}

func serveEmbeddedAsset(w http.ResponseWriter, r *http.Request, body []byte, contentType, etag string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

// GET /assets/world.svg（内嵌，避免依赖前端构建产物）
func (h *handlers) worldSVG(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedAsset(w, r, embeddedWorldSVG, "image/svg+xml; charset=utf-8", `"world-`+h.assetVersion()+`"`)
}

// GET /admin-app.js
func (h *handlers) adminAppJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedCompressed(w, r, embeddedAppJS, embeddedAppJSBr, embeddedAppJSGz,
		"application/javascript; charset=utf-8", `"app-`+h.assetVersion()+`-js"`)
}

// GET /xterm-app.js：远程终端按需加载，不阻塞首屏。
func (h *handlers) xtermAppJS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedCompressed(w, r, embeddedXtermJS, embeddedXtermJSBr, embeddedXtermJSGz,
		"application/javascript; charset=utf-8", `"xterm-`+h.assetVersion()+`"`)
}

// GET /admin-app.css
func (h *handlers) adminAppCSS(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedCompressed(w, r, embeddedAppCSS, embeddedAppCSSBr, embeddedAppCSSGz,
		"text/css; charset=utf-8", `"app-`+h.assetVersion()+`-css"`)
}

// serveEmbeddedCompressed 优先返回预压缩的 br/gzip，减少 Cloudflare 回源流量与首屏等待。
func serveEmbeddedCompressed(w http.ResponseWriter, r *http.Request, raw, br, gz []byte, contentType, etag string) {
	body, encoding := raw, ""
	ae := strings.ToLower(r.Header.Get("Accept-Encoding"))
	if len(br) > 0 && strings.Contains(ae, "br") {
		body, encoding = br, "br"
	} else if len(gz) > 0 && strings.Contains(ae, "gzip") {
		body, encoding = gz, "gzip"
	}
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	if encoding != "" {
		w.Header().Set("Content-Encoding", encoding)
		etag += "-" + encoding
	}
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	_, _ = w.Write(body)
}

// GET /assets/*：自定义构建目录的静态资源（不存在则 404）
func (h *handlers) pageAssets(w http.ResponseWriter, r *http.Request) {
	dist := strings.TrimSpace(h.cfg.WebDist)
	if dist == "" {
		http.NotFound(w, r)
		return
	}
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if strings.HasPrefix(clean, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(dist, clean)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, full)
}

// GET /favicon.svg、/favicon.ico
func (h *handlers) favicon(w http.ResponseWriter, r *http.Request) {
	serveEmbeddedAsset(w, r, embeddedFaviconSVG, "image/svg+xml; charset=utf-8", `"favicon-`+h.assetVersion()+`"`)
}
