package api

import (
	"net/http"
	"time"
)

// GET /api/admin/vpn/mesh — 全部服务器的组网状态（服务端 ↔ 节点端）。
//
// 组网与 VPN 绑定解耦：所有服务器都应上报组网/Agent 状态，只有绑定了
// VPN 节点的服务器才会显示节点域名与链路详情。
func (h *handlers) vpnAdminMesh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	settings, err := h.vpnStore().LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取设置失败")
		return
	}

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT s.id, s.name,
		        COALESCE(s.last_mesh_ip4, ''),
		        COALESCE(s.last_status, ''),
		        COALESCE(s.agent_version, ''),
		        COALESCE(s.agent_last_seen, 0),
		        COALESCE(n.id, 0), COALESCE(n.domain, ''), COALESCE(n.state, ''),
		        COALESCE(l.transport, ''), COALESCE(l.state, ''), COALESCE(l.rtt_ms, 0), COALESCE(l.last_seen, 0)
		   FROM servers s
		   LEFT JOIN vpn_nodes n ON n.server_id = s.id
		   LEFT JOIN vpn_node_links l ON l.node_id = n.id
		  ORDER BY s.id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询组网状态失败")
		return
	}
	defer rows.Close()

	type row struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		MeshIPv4      string `json:"mesh_ip4"`
		AgentStatus   string `json:"agent_status"`
		AgentVersion  string `json:"agent_version"`
		AgentLastSeen int64  `json:"agent_last_seen"`
		VPNNodeID     int64  `json:"vpn_node_id"`
		VPNDomain     string `json:"vpn_domain"`
		VPNState      string `json:"vpn_state"`
		Transport     string `json:"transport"`
		LinkState     string `json:"link_state"`
		RTTms         int    `json:"rtt_ms"`
		LastSeen      int64  `json:"last_seen"`
	}

	now := time.Now().UnixMilli()
	out := make([]row, 0, 32)
	for rows.Next() {
		var v row
		if err := rows.Scan(&v.ID, &v.Name, &v.MeshIPv4, &v.AgentStatus, &v.AgentVersion,
			&v.AgentLastSeen, &v.VPNNodeID, &v.VPNDomain, &v.VPNState,
			&v.Transport, &v.LinkState, &v.RTTms, &v.LastSeen); err != nil {
			continue
		}
		if v.Transport == "" {
			v.Transport = string(settings.ManagementTransport)
		}
		if v.LinkState == "" {
			v.LinkState = "down"
		}
		// Agent 2 分钟内有心跳即认为组网在线（WARP 或自建通道由 Agent 侧决定）。
		if v.AgentLastSeen > 0 && now-v.AgentLastSeen < 120000 {
			v.LinkState = "up"
			if v.LastSeen == 0 {
				v.LastSeen = v.AgentLastSeen
			}
		}
		out = append(out, v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]any{
			"mesh_ip4":    settings.ServerMeshIPv4,
			"mesh_domain": settings.ServerMeshDomain,
			"mesh_cidr4":  settings.MeshCIDR4,
			"transport":   settings.ManagementTransport,
			"fallback":    settings.MasqueFallback,
			"enabled":     settings.Enabled,
		},
		"nodes": out,
	})
}
