package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

type vpnNodeConfig struct {
	Listen      string   `json:"listen"`
	CertFile    string   `json:"cert_file"`
	KeyFile     string   `json:"key_file"`
	NodeID      int64    `json:"node_id"`
	MeshIPv4    string   `json:"mesh_ip4"`
	ClientCIDR  string   `json:"client_cidr4"`
	Gateway4    string   `json:"gateway_ip4"`
	Tun         string   `json:"tun"`
	MTU         int      `json:"mtu"`
	MaxDatagram int      `json:"max_datagram"`
	WAN         string   `json:"wan"`
	PublicKey   string   `json:"public_key"`
	Issuer      string   `json:"issuer"`
	Audience    string   `json:"audience"`
	DNS         []string `json:"dns"`
	Configure   bool     `json:"configure"`
}

// POST /api/admin/vpn/nodes/{id}/deploy
//
// 通过 Agent 执行一段固定脚本：下载 pp-masque-node、生成自签证书（若缺失）、
// 写配置与 systemd unit、重启服务。属于系统级 exec（system_action=vpn_apply）。
func (h *handlers) vpnAdminNodeDeploy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	var body struct {
		BinaryURL string `json:"binary_url"`
	}
	_ = decodeJSON(w, r, &body)

	node, err := h.vpnStore().GetNode(r.Context(), id)
	if err == vpn.ErrNotFound {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	if node.State != "active" || node.ClientCIDR == "" {
		writeError(w, http.StatusConflict, "请先启用节点（审批并分配地址池）")
		return
	}
	settings, err := h.vpnStore().LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取设置失败")
		return
	}
	signer, err := h.vpnSigner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签名密钥不可用")
		return
	}
	srv, err := h.fetchAgentServer(r.Context(), node.ServerID)
	if err != nil || srv == nil {
		writeError(w, http.StatusBadRequest, "节点对应的服务器不存在")
		return
	}

	host := strings.TrimSpace(node.Domain)
	if host == "" {
		host, _, err = net.SplitHostPort(node.Endpoint)
		if err != nil || host == "" {
			host = node.Endpoint
		}
	}
	if host == "" {
		writeError(w, http.StatusConflict, "节点未填写域名，无法生成证书")
		return
	}
	certPEM, keyPEM := "", ""
	if strings.EqualFold(node.CertMode, "internal_ca") {
		c, k, cerr := h.vpnIssueNodeCert(r.Context(), host)
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, "内部 CA 签发失败："+cerr.Error())
			return
		}
		certPEM, keyPEM = string(c), string(k)
	}
	cfg := vpnNodeConfig{
		Listen:      fmt.Sprintf("0.0.0.0:%d", node.ListenPort),
		CertFile:    "/etc/proxypilot/masque/tls/cert.pem",
		KeyFile:     "/etc/proxypilot/masque/tls/key.pem",
		NodeID:      node.ID,
		MeshIPv4:    node.MeshIPv4,
		ClientCIDR:  node.ClientCIDR,
		Gateway4:    cidrGateway(node.ClientCIDR),
		Tun:         "ppvpn0",
		MTU:         settings.MTU,
		MaxDatagram: settings.MTU + 80,
		PublicKey:   base64.StdEncoding.EncodeToString(signer.PublicKey()),
		Issuer:      "proxypilot",
		Audience:    "masque-node",
		DNS:         []string{cidrGateway(node.ClientCIDR)},
		Configure:   true,
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成配置失败")
		return
	}

	binaryURL := strings.TrimSpace(body.BinaryURL)
	if binaryURL == "" {
		binaryURL = strings.TrimRight(requestOrigin(r), "/") + "/agent-dist/pp-masque-node-linux-amd64"
	}
	script := buildVPNDeployScript(host, binaryURL, string(cfgJSON), node.ListenPort, certPEM, keyPEM)

	payload := map[string]any{
		"command":       script,
		"timeout":       300,
		"system_action": "vpn_apply",
		"service":       "proxypilot-masque",
	}
	taskID, _, msg := h.agentQueueTask(r.Context(), srv, "exec", 0, payload, actorOf(r))
	if taskID <= 0 {
		writeError(w, http.StatusInternalServerError, msg)
		return
	}
	h.vpnAudit(r.Context(), actorOf(r), "vpn.node.deploy", strconv.FormatInt(node.ID, 10), binaryURL)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "task_id": taskID, "binary_url": binaryURL, "server_id": node.ServerID,
	})
}

func buildVPNDeployScript(host, binaryURL, cfgJSON string, port int, certPEM, keyPEM string) string {
	certBlock := `if [ ! -s "$DIR/tls/cert.pem" ] || [ ! -s "$DIR/tls/key.pem" ]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 90 \
    -subj "/CN=` + strings.ReplaceAll(host, `"`, ``) + `" \
    -addext "subjectAltName=DNS:` + strings.ReplaceAll(host, `"`, ``) + `" \
    -keyout "$DIR/tls/key.pem" -out "$DIR/tls/cert.pem" >/dev/null 2>&1
  chmod 600 "$DIR/tls/key.pem"
fi`
	if certPEM != "" && keyPEM != "" {
		certBlock = `cat > "$DIR/tls/cert.pem" <<'PPCERT'
` + certPEM + `PPCERT
cat > "$DIR/tls/key.pem" <<'PPKEY'
` + keyPEM + `PPKEY
chmod 600 "$DIR/tls/key.pem"`
	}
	unit := `[Unit]
Description=ProxyPilot MASQUE Node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/proxypilot/masque/pp-masque-node -config /etc/proxypilot/masque/config.json
Restart=always
RestartSec=3
AmbientCapabilities=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_ADMIN CAP_NET_RAW CAP_NET_BIND_SERVICE
NoNewPrivileges=true
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
`
	return `set -euo pipefail
BIN=/opt/proxypilot/masque/pp-masque-node
DIR=/etc/proxypilot/masque
mkdir -p /opt/proxypilot/masque "$DIR/tls"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --retry 3 --connect-timeout 10 -o "$BIN.new" ` + shellQuote(binaryURL) + `
else
  wget -q -O "$BIN.new" ` + shellQuote(binaryURL) + `
fi
chmod +x "$BIN.new"
mv "$BIN.new" "$BIN"

` + certBlock + `

cat > "$DIR/config.json" <<'PPVPNCFG'
` + cfgJSON + `
PPVPNCFG

cat > /etc/systemd/system/proxypilot-masque.service <<'PPVPNUNIT'
` + unit + `PPVPNUNIT

systemctl daemon-reload
systemctl enable proxypilot-masque >/dev/null 2>&1 || true
systemctl restart proxypilot-masque
sleep 2
if ! systemctl is-active --quiet proxypilot-masque; then
  echo "pp-masque-node 启动失败："
  journalctl -u proxypilot-masque -n 30 --no-pager
  exit 1
fi
systemctl --no-pager --lines=5 status proxypilot-masque | head -8
ss -lunp 2>/dev/null | grep -E ':` + strconv.Itoa(port) + `\b' || true
`
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

