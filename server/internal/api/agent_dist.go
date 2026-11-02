package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func platformAssetKey(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	switch p {
	case "linux/arm64":
		return "linux-arm64"
	case "linux/amd64", "":
		return "linux-amd64"
	}
	return ""
}

func (h *handlers) agentDistDir() string {
	dir := strings.TrimSpace(h.cfg.AgentDistDir)
	if dir == "" {
		dir = "agent/dist"
	}
	return dir
}

func (h *handlers) readLocalManifest() (manifestData, error) {
	data := manifestData{Files: map[string]any{}}
	full := filepath.Join(h.agentDistDir(), "manifest.json")
	raw, err := os.ReadFile(full)
	if err != nil {
		return data, err
	}
	var j struct {
		Version string                     `json:"version"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return data, err
	}
	data.Version = j.Version
	for k, v := range j.Files {
		var anyVal any
		if json.Unmarshal(v, &anyVal) == nil {
			data.Files[k] = anyVal
		}
	}
	return data, nil
}

// GET /agent-dist/* —— 本地 Agent 分发（二进制 / manifest.json / SHA256SUMS）
func (h *handlers) agentDistFile(w http.ResponseWriter, r *http.Request) {
	clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/agent-dist/"))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(h.agentDistDir(), clean)
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeFile(w, r, full)
}
