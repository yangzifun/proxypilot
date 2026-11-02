package api

import (
	"encoding/base64"
	"net/http"
	"os"
	"strings"
)

func singboxRuntimeConfig() (string, error) {
	uuid := randomUUID()
	ssKey := randomB64Key()
	realityKey := randomRealityKey()
	shortID := mtprotoRandomHex(4)
	ssUser := singboxDeriveSSPassword(ssKey, uuid)
	tpl := &singboxTemplate{
		PortBase: 13700, WarpBase: 13800,
		CertPath: "/etc/sing-box/cert.crt", KeyPath: "/etc/sing-box/private.key",
		SSMethod: singboxDefaultSSMethod, SSServerKey: ssKey,
		RealityPrivateKey: realityKey, RealityShortID: shortID,
		RealityDest: "www.microsoft.com:443", RealitySNI: "www.microsoft.com",
		ProtocolOptions: "{}",
		EnableSS:        1, EnableAnyTLS: 1, EnableHY2: 1, EnableReality: 1,
	}
	users := []sbUser{{Name: "user01", UUID: uuid, SSPassword: ssUser}}
	ports := singboxPorts(tpl)
	cfg := singboxRenderSb(tpl, users, ports)
	return mustJSON(cfg), nil
}

// GET/POST /api/agent/bootstrap
func (h *handlers) agentBootstrap(w http.ResponseWriter, r *http.Request) {
	srv, status, msg := h.agentAuthenticate(r, nil)
	if srv == nil {
		writeJSON(w, status, map[string]any{"ok": false, "error": msg})
		return
	}
	archRaw := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("arch")))
	arch := "amd64"
	if archRaw == "arm64" || archRaw == "aarch64" {
		arch = "arm64"
	}
	content, err := singboxRuntimeConfig()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"ok": false, "error": "生成默认配置失败"})
		return
	}
	base := h.agentBaseFor(r)
	sha := strings.ToLower(strings.TrimSpace(os.Getenv("SINGBOX_SHA256_" + strings.ToUpper(arch))))
	version := strings.TrimSpace(os.Getenv("SINGBOX_VERSION"))
	binaryURL := ""
	if base != "" {
		binaryURL = base + "/sing-box-linux-" + arch
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "server_id": srv.ID, "server_name": srv.Name, "arch": arch,
		"binary_url": binaryURL, "binary_sha256": sha, "binary_version": version,
		"target": "/etc/sing-box/sb.json", "service": "sing-box",
		"cert_path": "/etc/sing-box/cert.crt", "key_path": "/etc/sing-box/private.key",
		"config_b64":  base64.StdEncoding.EncodeToString([]byte(content)),
		"server_time": nowMillis(),
	})
}
