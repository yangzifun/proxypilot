package api

import (
	"net/http"
	"time"
)

// GET /api/admin/vpn/mesh — 节点 ↔ 服务端组网状态（星型，服务端为中心）。
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
	nodes, err := h.vpnStore().ListNodes(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}

	type link struct {
		Transport string `json:"transport"`
		State     string `json:"state"`
		RTTms     int    `json:"rtt_ms"`
		LastSeen  int64  `json:"last_seen"`
	}
	links := map[int64]link{}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT node_id, transport, state, rtt_ms, last_seen FROM vpn_node_links`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id int64
			var l link
			if err := rows.Scan(&id, &l.Transport, &l.State, &l.RTTms, &l.LastSeen); err == nil {
				links[id] = l
			}
		}
	}

	type nodeView struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		ServerID  int64  `json:"server_id"`
		MeshIPv4  string `json:"mesh_ip4"`
		State     string `json:"state"`
		LastSeen  int64  `json:"last_seen"`
		Transport string `json:"transport"`
		LinkState string `json:"link_state"`
		RTTms     int    `json:"rtt_ms"`
		LinkSeen  int64  `json:"link_seen"`
	}
	now := time.Now()
	out := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		lastSeen := int64(0)
		if !n.LastSeen.IsZero() {
			lastSeen = n.LastSeen.UnixMilli()
		}
		v := nodeView{
			ID: n.ID, Name: n.Name, ServerID: n.ServerID, MeshIPv4: n.MeshIPv4,
			State: n.State, LastSeen: lastSeen,
			Transport: string(settings.ManagementTransport), LinkState: "down",
		}
		if l, ok := links[n.ID]; ok {
			v.Transport = l.Transport
			v.LinkState = l.State
			v.RTTms = l.RTTms
			v.LinkSeen = l.LastSeen
		} else if !n.LastSeen.IsZero() && now.Sub(n.LastSeen) < 2*time.Minute {
			v.LinkState = "up"
		}
		out = append(out, v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"server": map[string]any{
			"mesh_ip4":   settings.ServerMeshIPv4,
			"mesh_cidr4": settings.MeshCIDR4,
			"transport":  settings.ManagementTransport,
			"fallback":   settings.MasqueFallback,
			"enabled":    settings.Enabled,
		},
		"nodes": out,
	})
}


