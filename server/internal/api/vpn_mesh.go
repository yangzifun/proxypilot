package api

import (
	"net/http"
	"time"
)

// GET /api/admin/vpn/mesh — 自建组网状态（节点通过 vpncenter.yzfn.top 与中心组网）。
//
// 只展示已加入自建组网的服务器；WARP 等外部组网不在此展示。
// 组网与 VPN 绑定解耦：服务器可不绑定 VPN 节点，但仍可加入组网用于内网服务。
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
		`SELECT s.id, s.name, m.mesh_ip4, m.domain, m.state, m.transport, m.rtt_ms, m.last_seen,
		        COALESCE(s.agent_version, ''), COALESCE(s.agent_last_seen, 0)
		   FROM vpn_mesh_nodes m
		   JOIN servers s ON s.id = m.server_id
		  ORDER BY s.id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询组网状态失败")
		return
	}
	defer rows.Close()

	type member struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		MeshIPv4      string `json:"mesh_ip4"`
		Domain        string `json:"domain"`
		State         string `json:"state"`
		Transport     string `json:"transport"`
		RTTms         int    `json:"rtt_ms"`
		LastSeen      int64  `json:"last_seen"`
		AgentVersion  string `json:"agent_version"`
		AgentLastSeen int64  `json:"agent_last_seen"`
	}

	now := time.Now().UnixMilli()
	out := make([]member, 0, 32)
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.Name, &m.MeshIPv4, &m.Domain, &m.State, &m.Transport,
			&m.RTTms, &m.LastSeen, &m.AgentVersion, &m.AgentLastSeen); err != nil {
			continue
		}
		if m.LastSeen == 0 || now-m.LastSeen > 120000 {
			m.State = "down"
		}
		if m.State == "" {
			m.State = "down"
		}
		out = append(out, m)
	}

	var total int
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM servers`).Scan(&total)

	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]any{
			"mesh_ip4":    settings.ServerMeshIPv4,
			"mesh_domain": settings.ServerMeshDomain,
			"mesh_cidr4":  settings.MeshCIDR4,
			"transport":   settings.ManagementTransport,
			"fallback":    settings.MasqueFallback,
			"enabled":     settings.Enabled,
		},
		"total_servers": total,
		"joined":        len(out),
		"nodes":         out,
	})
}
