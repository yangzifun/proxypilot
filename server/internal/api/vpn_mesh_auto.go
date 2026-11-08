package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// autoMeshTask 在 Agent 轮询时自动为新服务器下发组网客户端（无需手工部署）。
func (h *handlers) autoMeshTask(ctx context.Context, srv *agentServer) {
	if srv == nil || strings.TrimSpace(h.settingValue(ctx, settingAgentMeshEnabled)) != "1" {
		return
	}
	var n int
	_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM vpn_mesh_nodes WHERE server_id = ?`, srv.ID).Scan(&n)
	if n > 0 {
		return
	}
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE server_id = ? AND status IN ('pending','sent') AND payload LIKE '%pp-mesh-client%'`,
		srv.ID).Scan(&n)
	if n > 0 {
		return
	}

	ip := fmt.Sprintf("10.99.1.%d", srv.ID%200+20)
	signer, err := h.vpnSigner(ctx)
	if err != nil {
		return
	}
	ttl := 8760 * time.Hour
	token, err := signer.Issue(vpn.Claims{
		Subject: fmt.Sprintf("mesh:%d", srv.ID), NodeID: srv.ID, IPv4: ip, Network: "mesh",
	}, ttl)
	if err != nil {
		return
	}

	unit := fmt.Sprintf(`[Unit]
Description=ProxyPilot mesh client
After=network-online.target
[Service]
Type=simple
ExecStart=/bin/sh -c "exec /opt/proxypilot/mesh/pp-mesh-client -mesh-token \"$(cat /etc/proxypilot/mesh.token)\" -mesh-ip %s -mesh-cidr 10.99.0.0/16 -mesh-tun ppmesh0 -mesh-endpoint vpncenter.yzfn.top:8443 -mesh-sni vpncenter.yzfn.top -insecure"
Restart=always
RestartSec=3
[Install]
WantedBy=multi-user.target`, ip)
	script := fmt.Sprintf(`set -e
mkdir -p /opt/proxypilot/mesh /etc/proxypilot
curl -fsSL https://proxypilot.yangzifun.org/agent-dist/pp-mesh-client -o /opt/proxypilot/mesh/pp-mesh-client
printf '%%s' %s > /etc/proxypilot/mesh.token
chmod 755 /opt/proxypilot/mesh/pp-mesh-client
chmod 600 /etc/proxypilot/mesh.token
cat > /etc/systemd/system/pp-mesh-client.service <<'UNIT'
%s
UNIT
systemctl daemon-reload
systemctl reset-failed pp-mesh-client 2>/dev/null || true
systemctl enable pp-mesh-client
systemctl restart pp-mesh-client
sleep 3
systemctl is-active pp-mesh-client`, token, unit)

	payload := map[string]any{
		"command":       script,
		"timeout":       90,
		"system_action": "vpn_apply",
		"service":       "pp-mesh-client",
	}
	if _, _, msg := h.agentQueueTask(ctx, srv, "exec", 0, payload, "auto-mesh"); msg != "" {
		h.log.Warn("自动组网任务入队失败", "server", srv.ID, "err", msg)
		return
	}
	now := time.Now().UnixMilli()
	_, _ = h.db.ExecContext(ctx,
		`INSERT INTO vpn_mesh_nodes (server_id, mesh_ip4, domain, state, transport, rtt_ms, last_seen, updated_at)
		 VALUES (?, ?, 'vpncenter.yzfn.top', 'up', 'masque', 0, ?, ?)
		 ON DUPLICATE KEY UPDATE mesh_ip4 = VALUES(mesh_ip4), state = 'up', last_seen = VALUES(last_seen), updated_at = VALUES(updated_at)`,
		srv.ID, ip, now, now)
}
