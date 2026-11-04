package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// ---------------- mihomo 服务端模式（listeners） ----------------

type mihomoServerConfig struct {
	Protocols   []string `json:"protocols"`
	PortBase    int      `json:"port_base"`
	Listen      string   `json:"listen"`
	WSPath      string   `json:"ws_path"`
	SSMethod    string   `json:"ss_method"`
	CertPath    string   `json:"cert_path"`
	KeyPath     string   `json:"key_path"`
	RealityDest string   `json:"reality_dest"`
	RealityPriv string   `json:"reality_private_key"`
	RealitySID  string   `json:"reality_short_id"`
	RealitySNI  []string `json:"reality_server_names"`
	Secret      string   `json:"secret"`
}

func defaultMihomoServerConfig() mihomoServerConfig {
	return mihomoServerConfig{
		Protocols:   []string{"vless_ws", "vless_reality", "vmess", "trojan", "anytls", "hy2", "tuic"},
		PortBase:    13800,
		WSPath:      "/vlws",
		Listen:      "::",
		SSMethod:    "2022-blake3-aes-256-gcm",
		CertPath:    "/etc/mihomo/cert.crt",
		KeyPath:     "/etc/mihomo/private.key",
		RealityDest: "www.microsoft.com:443",
		RealitySNI:  []string{"www.microsoft.com"},
	}
}

func parseMihomoServerConfig(raw string) mihomoServerConfig {
	cfg := defaultMihomoServerConfig()
	if strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	if cfg.PortBase == 0 {
		cfg.PortBase = 13800
	}
	if strings.TrimSpace(cfg.WSPath) == "" {
		cfg.WSPath = "/vlws"
	}
	if strings.TrimSpace(cfg.Listen) == "" {
		cfg.Listen = "::"
	}
	if strings.TrimSpace(cfg.CertPath) == "" {
		cfg.CertPath = "/etc/mihomo/cert.crt"
	}
	if strings.TrimSpace(cfg.KeyPath) == "" {
		cfg.KeyPath = "/etc/mihomo/private.key"
	}
	if strings.TrimSpace(cfg.SSMethod) == "" {
		cfg.SSMethod = "2022-blake3-aes-256-gcm"
	}
	return cfg
}

func mihomoQuote(v string) string {
	return strconv.Quote(v)
}

// renderMihomoServer 生成 mihomo 服务端配置（listeners）。
func renderMihomoServer(cfg mihomoServerConfig, uuids []string) (string, map[string]any, string) {
	if len(uuids) == 0 {
		return "", nil, "没有授权 UUID，无法生成服务端配置"
	}
	protoSet := map[string]bool{}
	for _, p := range cfg.Protocols {
		protoSet[p] = true
	}
	base := cfg.PortBase
	port := func(offset int) int { return base + offset }

	var b strings.Builder
	b.WriteString("# 由 ProxyPilot 生成，请勿手工修改\n")
	b.WriteString("mixed-port: 7890\nsocks-port: 7891\nallow-lan: true\nmode: rule\nlog-level: warning\n")
	b.WriteString("external-controller: 127.0.0.1:9090\n")
	if strings.TrimSpace(cfg.Secret) != "" {
		b.WriteString("secret: " + mihomoQuote(cfg.Secret) + "\n")
	}
	b.WriteString("listeners:\n")

	ports := map[string]int{}
	addUsersUUID := func(indent string) {
		for _, u := range uuids {
			b.WriteString(indent + "- username: " + mihomoQuote(u) + "\n")
			b.WriteString(indent + "  uuid: " + mihomoQuote(u) + "\n")
		}
	}
	addUsersPass := func(indent string) {
		for _, u := range uuids {
			b.WriteString(indent + "- username: " + mihomoQuote(u) + "\n")
			b.WriteString(indent + "  password: " + mihomoQuote(u) + "\n")
		}
	}
	addUsersMap := func(indent string) {
		for _, u := range uuids {
			b.WriteString(indent + mihomoQuote(u) + ": " + mihomoQuote(u) + "\n")
		}
	}

	if protoSet["vless_ws"] {
		p := port(6)
		ports["vless_ws"] = p
		b.WriteString("  - name: vless-ws\n    type: vless\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    ws-path: " + mihomoQuote(cfg.WSPath) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersUUID("      ")
	}
	if protoSet["vless_reality"] {
		p := port(5)
		ports["vless_reality"] = p
		b.WriteString("  - name: vless-reality\n    type: vless\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    reality-config:\n")
		b.WriteString("      dest: " + mihomoQuote(cfg.RealityDest) + "\n")
		b.WriteString("      private-key: " + mihomoQuote(cfg.RealityPriv) + "\n")
		if strings.TrimSpace(cfg.RealitySID) != "" {
			b.WriteString("      short-id:\n        - " + mihomoQuote(cfg.RealitySID) + "\n")
		}
		if len(cfg.RealitySNI) > 0 {
			b.WriteString("      server-names:\n")
			for _, sn := range cfg.RealitySNI {
				b.WriteString("        - " + mihomoQuote(sn) + "\n")
			}
		}
		b.WriteString("    users:\n")
		addUsersUUID("      ")
	}
	if protoSet["vmess"] {
		p := port(7)
		ports["vmess"] = p
		b.WriteString("  - name: vmess\n    type: vmess\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    ws-path: " + mihomoQuote(cfg.WSPath) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersUUID("      ")
	}
	if protoSet["trojan"] {
		p := port(8)
		ports["trojan"] = p
		b.WriteString("  - name: trojan\n    type: trojan\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersPass("      ")
	}
	if protoSet["anytls"] {
		p := port(3)
		ports["anytls"] = p
		b.WriteString("  - name: anytls\n    type: anytls\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersMap("      ")
	}
	if protoSet["hy2"] {
		p := port(1)
		ports["hy2"] = p
		b.WriteString("  - name: hysteria2\n    type: hysteria2\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersMap("      ")
	}
	if protoSet["tuic"] {
		p := port(4)
		ports["tuic"] = p
		b.WriteString("  - name: tuic\n    type: tuic\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    certificate: " + mihomoQuote(cfg.CertPath) + "\n")
		b.WriteString("    private-key: " + mihomoQuote(cfg.KeyPath) + "\n")
		b.WriteString("    users:\n")
		addUsersMap("      ")
	}
	if protoSet["socks"] {
		p := port(9)
		ports["socks"] = p
		b.WriteString("  - name: socks5\n    type: socks\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    users:\n")
		addUsersMap("      ")
	}
	if protoSet["http"] {
		p := port(10)
		ports["http"] = p
		b.WriteString("  - name: http\n    type: http\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    users:\n")
		addUsersMap("      ")
	}
	if protoSet["ss"] {
		p := port(2)
		ports["ss"] = p
		b.WriteString("  - name: shadowsocks\n    type: shadowsocks\n")
		b.WriteString("    port: " + strconv.Itoa(p) + "\n    listen: " + mihomoQuote(cfg.Listen) + "\n")
		b.WriteString("    cipher: " + mihomoQuote(cfg.SSMethod) + "\n")
		b.WriteString("    password: " + mihomoQuote(uuids[0]) + "\n")
	}

	b.WriteString("\nproxies:\n\nproxy-groups:\n\nrules:\n  - MATCH,DIRECT\n")
	if len(ports) == 0 {
		return "", nil, "未选择任何协议"
	}
	return b.String(), map[string]any{"ports": ports, "users": len(uuids), "protocols": cfg.Protocols}, ""
}

// POST /api/mihomo/nodes/{id}/render  生成服务端配置
func (h *handlers) mihomoNodeRender(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	var (
		name       string
		serverID   int64
		templateID int64
		uuidRaw    string
		mode       string
		serverRaw  string
		adminRaw   string
		portBase   int64
		domain     string
		configPath string
		service    string
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]'), COALESCE(n.port_base,0),
		        COALESCE(n.domain,''), COALESCE(n.config_path,'/etc/mihomo/config.yaml'), COALESCE(n.service,'mihomo'),
		        COALESCE(t.mode,'subscription'), COALESCE(t.server_json,'{}')
		   FROM mihomo_nodes n LEFT JOIN mihomo_templates t ON t.id = n.template_id
		  WHERE n.id = ? LIMIT 1`, id).
		Scan(&name, &serverID, &templateID, &uuidRaw, &portBase, &domain, &configPath, &service, &mode, &serverRaw)
	if err != nil {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	if mode != "server" {
		writeError(w, http.StatusBadRequest, "该模板不是服务端模板，请在模板里切换为「服务端」")
		return
	}
	cfg := parseMihomoServerConfig(serverRaw)
	if portBase > 0 {
		cfg.PortBase = int(portBase)
	}
	uuids := mihomoParseUUIDs(uuidRaw)
	adminRaw = "" // reserved
	_ = adminRaw
	yamlText, stats, msg := renderMihomoServer(cfg, uuids)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	sum := sha256.Sum256([]byte(yamlText))
	hash := hex.EncodeToString(sum[:])
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"name": name, "server_id": serverID, "template_id": templateID,
			"mode": mode, "yaml": yamlText, "hash": hash,
			"ports": stats["ports"], "users": stats["users"],
			"config_path": configPath, "service": service, "domain": domain,
			"filename": fmt.Sprintf("mihomo-%d.yaml", id),
		},
	})
}

// POST /api/mihomo/templates/preview-server  按模板参数生成示例服务端配置（用于模板编辑预览）
func (h *handlers) mihomoTemplatePreviewServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServerJSON string   `json:"server_json"`
		UUIDs      []string `json:"uuids"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	cfg := parseMihomoServerConfig(body.ServerJSON)
	uuids := body.UUIDs
	if len(uuids) == 0 {
		uuids = []string{"11111111-1111-4111-8111-111111111111"}
	}
	yamlText, stats, msg := renderMihomoServer(cfg, uuids)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"yaml": yamlText, "ports": stats["ports"], "users": stats["users"]},
	})
}
