package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// bootFromHTML 从服务端渲染的 HTML 中解析 #pp-boot JSON 数据块。
// 强制 CSP 下动态数据不再写成可执行内联脚本（window.__PP_BOOT__=...），而是
// <script type="application/json" id="pp-boot"> 数据块，由外部 /boot.js 解析。
func bootFromHTML(t *testing.T, body string) map[string]any {
	t.Helper()
	const marker = `<script type="application/json" id="pp-boot">`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("缺少 #pp-boot JSON 数据块")
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, "</script>")
	if end < 0 {
		t.Fatalf("#pp-boot 数据块未闭合")
	}
	var boot map[string]any
	if err := json.Unmarshal([]byte(rest[:end]), &boot); err != nil {
		t.Fatalf("#pp-boot 不是合法 JSON：%v", err)
	}
	return boot
}

func TestPageShellModes(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}}

	// portal
	rr := httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != 200 {
		t.Fatalf("/ status = %d", rr.Code)
	}
	body := rr.Body.String()
	boot := bootFromHTML(t, body)
	if boot["mode"] != "portal" || !strings.Contains(body, "/admin-app.js") {
		t.Fatalf("portal shell 内容异常: %s", body[:min(200, len(body))])
	}

	// admin
	rr = httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body = rr.Body.String()
	boot = bootFromHTML(t, body)
	if boot["mode"] != "admin" || boot["superAdmin"] != "admin" {
		t.Fatalf("admin shell 内容异常: %s", body[:min(200, len(body))])
	}
	if boot["flavor"] != "antd" {
		t.Fatalf("boot = %#v", boot)
	}
	// 外壳必须引用外部引导脚本，且不能有可执行内联脚本
	if !strings.Contains(body, `src="/boot.js"`) {
		t.Fatal("缺少外部引导脚本 /boot.js")
	}
	if hasExecutableInlineScript(body) {
		t.Fatal("外壳不应包含可执行内联脚本")
	}

	// /login → /admin
	rr = httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/admin" {
		t.Fatalf("login 重定向 = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// /manage → /
	rr = httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/manage", nil))
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/" {
		t.Fatalf("manage 重定向 = %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

// 加载动画里的版本号必须与服务端版本（api.Version）一致，
// 不能再出现「加载页显示旧版本」的情况。
func TestBootScreenVersionMatchesServer(t *testing.T) {
	old := Version
	Version = "V99.9"
	defer func() { Version = old }()

	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}}
	rr := httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body := rr.Body.String()

	if !strings.Contains(body, `pp-boot-version">V99.9<`) {
		t.Fatalf("加载动画版本未使用服务端版本")
	}
	if !strings.Contains(body, `<meta name="pp-version" content="V99.9">`) {
		t.Fatalf("缺少 pp-version meta 兜底")
	}
	boot := bootFromHTML(t, body)
	if boot["version"] != "V99.9" {
		t.Fatalf("boot.version = %v，期望 V99.9", boot["version"])
	}
}

// 未注入服务端版本时应回退到前端 meta 版本，而不是写死某个旧版本。
func TestBootScreenVersionFallsBackToMeta(t *testing.T) {
	old := Version
	Version = ""
	defer func() { Version = old }()

	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}}
	meta := h.webMetaInfo().Version
	rr := httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body := rr.Body.String()

	if meta != "" && !strings.Contains(body, `pp-boot-version">`+meta+`<`) {
		t.Fatalf("未注入版本时未回退到 meta 版本 %q", meta)
	}
}

func TestEmbeddedAssets(t *testing.T) {
	h := &handlers{}
	rr := httptest.NewRecorder()
	h.adminAppJS(rr, httptest.NewRequest(http.MethodGet, "/admin-app.js", nil))
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("js = %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if rr.Body.Len() < 1000 {
		t.Fatalf("js 体积异常: %d", rr.Body.Len())
	}

	rr = httptest.NewRecorder()
	h.worldSVG(rr, httptest.NewRequest(http.MethodGet, "/assets/world.svg", nil))
	if rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Type"), "svg") {
		t.Fatalf("svg = %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
	if !strings.Contains(rr.Body.String(), "<svg") {
		t.Fatal("world.svg 内容异常")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}