package api

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
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
	// 节点在网关 53 上跑轻量 DNS 转发，客户端解析就交给它
	DNSRelay    bool     `json:"dns_relay"`
	DNSUpstream []string `json:"dns_upstream"`
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
	switch strings.ToLower(strings.TrimSpace(node.CertMode)) {
	case "internal_ca":
		// 平台内部 CA：直接给节点签发证书（客户端需信任 /api/vpn/ca.crt）
		c, k, cerr := h.vpnIssueNodeCert(r.Context(), host)
		if cerr != nil {
			writeError(w, http.StatusInternalServerError, "内部 CA 签发失败："+cerr.Error())
			return
		}
		certPEM, keyPEM = string(c), string(k)
	case "acme":
		// 公网 ACME：使用「证书管理」里已签发的通配证书，先校验域名是否被覆盖
		stored, cerr := h.loadCert(r.Context())
		if cerr != nil || stored == nil || strings.TrimSpace(stored.CertPEM) == "" {
			writeError(w, http.StatusConflict, "尚未签发公网证书，请先到「证书管理」签发后再部署")
			return
		}
		if !certCoversHost(stored.CertPEM, host) {
			writeError(w, http.StatusConflict,
				fmt.Sprintf("已签发的证书不覆盖域名 %s，请到「证书管理」重新签发包含该域名的证书", host))
			return
		}
		certPEM, keyPEM = stored.CertPEM, stored.KeyPEM
		if strings.TrimSpace(stored.ChainPEM) != "" {
			certPEM = certPEM + "\n" + stored.ChainPEM
		}
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
		// 客户端把 DNS 指向节点网关，节点必须真的在那里应答，
		// 否则客户端连上后所有域名解析都会超时。
		DNSRelay:    true,
		DNSUpstream: []string{"1.1.1.1", "223.5.5.5"},
		Configure:   true,
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成配置失败")
		return
	}

	// MASQUE 节点已内嵌在 Agent 中，部署不再下载二进制；body.BinaryURL 仅为兼容旧客户端保留
	binaryURL := strings.TrimSpace(body.BinaryURL)
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
		"ok": true, "task_id": taskID, "server_id": node.ServerID, "embedded": true,
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
	// MASQUE 节点现在内嵌在 Agent 进程里，不再单独下载二进制：
	// 只需要把证书与配置写到约定位置，然后重启 Agent 让内嵌节点重新加载。
	return `set -euo pipefail
DIR=/etc/proxypilot/masque
AGENT_DIR=/etc/proxypilot/agent
mkdir -p "$DIR/tls" "$AGENT_DIR"

` + certBlock + `

cat > "$AGENT_DIR/vpn.json" <<'PPVPNCFG'
` + cfgJSON + `
PPVPNCFG
chmod 644 "$AGENT_DIR/vpn.json"

# 旧版独立节点服务若存在则停掉，避免与内嵌节点抢 UDP 443
if systemctl list-unit-files 2>/dev/null | grep -q '^proxypilot-masque\.service'; then
  systemctl disable --now proxypilot-masque >/dev/null 2>&1 || true
fi

# 异步重启 Agent：本任务本身就由 Agent 执行，同步重启会掐断任务回报
nohup sh -c 'sleep 1; systemctl restart proxypilot-agent' >/dev/null 2>&1 &
echo "VPN 节点配置已写入 $AGENT_DIR/vpn.json，证书已更新，Agent 将在 1 秒后重启"
`
}

// certCoversHost 判断 PEM 证书是否覆盖给定域名（精确匹配或通配 *.example.com）。
func certCoversHost(certPEM, host string) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	if err := cert.VerifyHostname(host); err == nil {
		return true
	}
	for _, name := range cert.DNSNames {
		n := strings.ToLower(name)
		if strings.HasPrefix(n, "*.") && strings.HasSuffix(host, n[1:]) {
			// 通配只匹配一级
			if strings.Count(host, ".") == strings.Count(n[1:], ".")+1 {
				return true
			}
		}
	}
	return false
}
