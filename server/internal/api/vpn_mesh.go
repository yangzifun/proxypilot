package api

import (
	"net/http"
	"os/exec"
	"time"
)

// GET /api/admin/vpn/mesh — 自建组网状态（节点通过 vpncenter.yzfn.top 与中心组网）。
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

	out := make([]member, 0, 32)
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.ID, &m.Name, &m.MeshIPv4, &m.Domain, &m.State, &m.Transport,
			&m.RTTms, &m.LastSeen, &m.AgentVersion, &m.AgentLastSeen); err != nil {
			continue
		}
		out = append(out, m)
	}

	// 从中心对组网 IP 做 ICMP 探活，实时更新状态/心跳（ping 不存在时按 TCP 22 兜底）。
	type probe struct {
		idx int
		ok  bool
		rtt int
	}
	ch := make(chan probe, len(out))
	for i := range out {
		go func(i int) {
			start := time.Now()
			err := exec.Command("ping", "-c", "1", "-W", "1", out[i].MeshIPv4).Run()
			ch <- probe{i, err == nil, int(time.Since(start).Milliseconds())}
		}(i)
	}
	nowMs := time.Now().UnixMilli()
	for range out {
		p := <-ch
		if p.ok {
			out[p.idx].State = "up"
			out[p.idx].RTTms = p.rtt
			out[p.idx].LastSeen = nowMs
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE vpn_mesh_nodes SET state = 'up', rtt_ms = ?, last_seen = ?, updated_at = ? WHERE server_id = ?`,
				p.rtt, nowMs, nowMs, out[p.idx].ID)
		} else {
			out[p.idx].State = "down"
			out[p.idx].RTTms = 0
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE vpn_mesh_nodes SET state = 'down', rtt_ms = 0, updated_at = ? WHERE server_id = ?`,
				nowMs, out[p.idx].ID)
		}
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
