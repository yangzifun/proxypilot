package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestLocalAgentDist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"),
		[]byte(`{"version":"v9.9.9","files":{"linux-amd64":{"file":"proxypilot-agent-linux-amd64","sha256":"ab"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h := &handlers{cfg: config.Config{AgentDistDir: dir}}

	m, err := h.readLocalManifest()
	if err != nil {
		t.Fatalf("readLocalManifest: %v", err)
	}
	if m.Version != "v9.9.9" {
		t.Fatalf("version = %q", m.Version)
	}
	if m.Files["linux-amd64"] == nil {
		t.Fatalf("files = %#v", m.Files)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent-dist/manifest.json", nil)
	h.agentDistFile(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.Len() == 0 {
		t.Fatal("响应为空")
	}

	rr2 := httptest.NewRecorder()
	h.agentDistFile(rr2, httptest.NewRequest(http.MethodGet, "/agent-dist/../secret", nil))
	if rr2.Code == http.StatusOK {
		t.Fatal("路径穿越应被拒绝")
	}
}
