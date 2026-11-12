package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestAgentInstallScriptRender(t *testing.T) {
	h := &handlers{cfg: config.Config{AgentBinaryBaseURL: "https://bin.example.com/agent/"}}
	req := httptest.NewRequest(http.MethodGet, "http://pp.example.com/api/agent/install.sh", nil)
	req.Host = "pp.example.com"
	req.RemoteAddr = "127.0.0.1:12345" // 可信反代，允许 X-Forwarded-Proto
	req.Header.Set("X-Forwarded-Proto", "http")
	rec := httptest.NewRecorder()
	h.agentInstallScript(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "x-shellscript") {
		t.Fatalf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ORIGIN='http://pp.example.com'") {
		t.Fatal("未注入 ORIGIN")
	}
	if !strings.Contains(body, "https://bin.example.com/agent") {
		t.Fatal("未注入 BINARY_BASE")
	}
	if strings.Contains(body, "your-worker.example.com") {
		t.Fatal("仍含示例域名")
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatal("脚本应以换行结尾")
	}
}
