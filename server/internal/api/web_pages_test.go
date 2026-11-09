package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestPageShellModes(t *testing.T) {
	h := &handlers{cfg: config.Config{SuperAdmin: "admin"}}

	// portal
	rr := httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != 200 {
		t.Fatalf("/ status = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"mode":"portal"`) || !strings.Contains(body, "/admin-app.js") {
		t.Fatalf("portal shell 内容异常: %s", body[:min(200, len(body))])
	}

	// admin
	rr = httptest.NewRecorder()
	h.pageIndex(rr, httptest.NewRequest(http.MethodGet, "/admin", nil))
	body = rr.Body.String()
	if !strings.Contains(body, `"mode":"admin"`) || !strings.Contains(body, `"superAdmin":"admin"`) {
		t.Fatalf("admin shell 内容异常")
	}
	var boot map[string]any
	start := strings.Index(body, "window.__PP_BOOT__=")
	if start < 0 {
		t.Fatal("缺少 boot 注入")
	}
	snippet := body[start+len("window.__PP_BOOT__="):]
	snippet = snippet[:strings.Index(snippet, ";</script>")]
	if err := json.Unmarshal([]byte(snippet), &boot); err != nil {
		t.Fatalf("boot JSON 解析失败: %v", err)
	}
	if boot["mode"] != "admin" || boot["flavor"] != "antd" {
		t.Fatalf("boot = %#v", boot)
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
	var boot map[string]any
	start := strings.Index(body, "window.__PP_BOOT__=")
	if start < 0 {
		t.Fatal("缺少 boot 注入")
	}
	snippet := body[start+len("window.__PP_BOOT__="):]
	snippet = snippet[:strings.Index(snippet, ";</script>")]
	if err := json.Unmarshal([]byte(snippet), &boot); err != nil {
		t.Fatalf("boot JSON 解析失败: %v", err)
	}
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
