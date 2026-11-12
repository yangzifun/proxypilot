package api

import (
	_ "embed"
	"net/http"
	"regexp"
	"strings"
)

// 页面脚本全部改为同源外部文件：强制 CSP 只放行 script-src 'self'（无 nonce、无
// unsafe-inline），因此 HTML 里不能再出现可执行内联脚本。这样 CSP 头对所有请求
// 完全一致，Cloudflare/缓存拆分响应也不会造成「nonce 与 HTML 不匹配 → 白屏」。
var (
	//go:embed assets_web/boot.js
	embeddedBootJS []byte
	//go:embed assets_web/client-auth.js
	embeddedClientAuthJS []byte
	//go:embed assets_web/admin-settings.js
	embeddedAdminSettingsJS []byte
	//go:embed assets_web/admin-certs.js
	embeddedAdminCertsJS []byte
)

// 页面脚本路径（固定）。内容由 Go embed 提供；HTML 里只出现这些同源外部脚本。
const (
	pageScriptBoot          = "/boot.js"
	pageScriptClientAuth    = "/client-auth.js"
	pageScriptAdminSettings = "/admin-settings.js"
	pageScriptAdminCerts    = "/admin-certs.js"
)

// servePageScript 输出小型页面脚本。
// 使用 no-store 而不是长缓存：这些文件只有几 KB，但内容会随服务端版本变化，
// 若被 CDN/浏览器缓存成旧版本，可能和 HTML 的数据约定不一致（同类缓存问题正是
// 本次白屏事故的诱因），因此优先保证版本一致性。
func servePageScript(w http.ResponseWriter, r *http.Request, body []byte) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// GET /boot.js —— /admin、/（门户）外壳的引导脚本（解析 #pp-boot 数据块 + 加载失败提示）。
func (h *handlers) bootJS(w http.ResponseWriter, r *http.Request) {
	servePageScript(w, r, embeddedBootJS)
}

// GET /client-auth.js —— /client-auth 授权页脚本。
func (h *handlers) clientAuthJS(w http.ResponseWriter, r *http.Request) {
	servePageScript(w, r, embeddedClientAuthJS)
}

// GET /admin-settings.js —— /admin/settings 设置页脚本。
func (h *handlers) adminSettingsJS(w http.ResponseWriter, r *http.Request) {
	servePageScript(w, r, embeddedAdminSettingsJS)
}

// GET /admin-certs.js —— /admin/certs 证书页脚本。
func (h *handlers) adminCertsJS(w http.ResponseWriter, r *http.Request) {
	servePageScript(w, r, embeddedAdminCertsJS)
}

// htmlScript 一个 <script> 标签的属性串与标签体（用于 CSP 自检/自定义外壳校验）。
type htmlScript struct {
	attrs string
	body  string
}

var (
	scriptTagRe   = regexp.MustCompile(`(?i)<script`)
	scriptSrcRe   = regexp.MustCompile(`(?i)(?:^|\s)src\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	scriptTypeRe  = regexp.MustCompile(`(?i)(?:^|\s)type\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	inlineHandler = regexp.MustCompile(`(?i)<[a-z][^>]*\son[a-z]+\s*=`)
)

// parseHTMLScripts 粗粒度解析 HTML 中的 <script> 标签（只用于本站自产页面与自定义
// index.html 的 CSP 兜底检查，不追求完整 HTML 解析）。
func parseHTMLScripts(page string) []htmlScript {
	locs := scriptTagRe.FindAllStringIndex(page, -1)
	out := make([]htmlScript, 0, len(locs))
	lower := strings.ToLower(page)
	skipUntil := 0
	for _, loc := range locs {
		// "<script" 可能出现在上一个脚本体内（如 JS 字符串），此时按标签体内容处理。
		if loc[0] < skipUntil {
			continue
		}
		gt := strings.Index(page[loc[1]:], ">")
		if gt < 0 {
			break
		}
		tagEnd := loc[1] + gt
		attrs := page[loc[1]:tagEnd]
		bodyStart := tagEnd + 1
		if rel := strings.Index(lower[bodyStart:], "</script"); rel >= 0 {
			out = append(out, htmlScript{attrs: attrs, body: page[bodyStart : bodyStart+rel]})
			skipUntil = bodyStart + rel + len("</script")
		} else {
			out = append(out, htmlScript{attrs: attrs, body: page[bodyStart:]})
			skipUntil = len(page)
		}
	}
	return out
}

// scriptAttrValue 从标签属性串里取属性值（大小写不敏感，兼容引号/无引号写法）。
func scriptAttrValue(attrs, name string) string {
	var re *regexp.Regexp
	switch strings.ToLower(name) {
	case "src":
		re = scriptSrcRe
	case "type":
		re = scriptTypeRe
	default:
		return ""
	}
	m := re.FindStringSubmatch(attrs)
	if m == nil {
		return ""
	}
	for _, v := range m[1:] {
		if v != "" {
			return v
		}
	}
	return ""
}

// scriptTypeIsExecutable 判断 script type 是否可执行（缺省 = classic script = 可执行）。
func scriptTypeIsExecutable(attrs string) bool {
	typ := strings.ToLower(strings.TrimSpace(scriptAttrValue(attrs, "type")))
	switch typ {
	case "", "text/javascript", "application/javascript", "application/ecmascript",
		"text/ecmascript", "module":
		return true
	// 数据块类型：浏览器不执行，CSP 也不按 script-src 拦截。
	case "application/json", "application/ld+json", "importmap", "speculationrules",
		"text/template", "text/x-template", "text/plain":
		return false
	}
	return false
}

// hasExecutableInlineScript 判断 HTML 里是否存在「没有 src 的可执行脚本」。
// 强制 CSP（script-src 'self'，无 unsafe-inline/nonce/hash）下这种脚本一定会被拦。
func hasExecutableInlineScript(page string) bool {
	for _, s := range parseHTMLScripts(page) {
		if strings.TrimSpace(scriptAttrValue(s.attrs, "src")) != "" {
			continue
		}
		if scriptTypeIsExecutable(s.attrs) {
			return true
		}
	}
	return false
}