package api

import (
	"net/http"
	"strings"
	"time"
)

// POST /api/agent/vpn/register — 节点首次注册/重新注册。
func (h *handlers) vpnAgentRegister(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil || body == nil {
		body = map[string]any{}
	}
	srv, status, msg := h.agentAuthenticate(r, body)
	if srv == nil {
		writeError(w, status, msg)
		return
	}

	name := jsonStr(body["name"])
	if name == "" {
		name = srv.Name
	}
	endpoint := strings.TrimSpace(jsonStr(body["endpoint"]))
	pubKey := strings.TrimSpace(jsonStr(body["pubkey"]))
	version := strings.TrimSpace(jsonStr(body["version"]))

	node, err := h.vpnStore().UpsertNodeRegistration(r.Context(), srv.ID, name, endpoint, pubKey, version)
	if err != nil {
		h.log.Error("VPN 节点注册失败", "err", err, "server_id", srv.ID)
		writeError(w, http.StatusInternalServerError, "节点注册失败")
		return
	}
	settings, err := h.vpnStore().LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 VPN 设置失败")
		return
	}
	warpOK := false
	if v, ok := body["warp"].(bool); ok {
		warpOK = v
	}

	h.vpnAudit(r.Context(), "agent:"+srv.Name, "vpn.node.register", node.Name, endpoint)
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":               node.ID,
		"state":                 node.State,
		"mesh_ip4":              node.MeshIPv4,
		"client_cidr4":          node.ClientCIDR,
		"management_transport":  settings.ManagementTransport,
		"effective_transport":   settings.EffectiveTransport(warpOK),
		"masque_fallback":       settings.MasqueFallback,
		"server_mesh_ip4":       settings.ServerMeshIPv4,
		"mtu":                   settings.MTU,
		"token_ttl_seconds":     settings.TokenTTLSeconds,
		"masque_enabled":        settings.Enabled,
	})
}

// POST /api/agent/vpn/heartbeat — 节点心跳与链路状态。
func (h *handlers) vpnAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil || body == nil {
		body = map[string]any{}
	}
	srv, status, msg := h.agentAuthenticate(r, body)
	if srv == nil {
		writeError(w, status, msg)
		return
	}

	node, err := h.vpnStore().GetNodeByServer(r.Context(), srv.ID)
	if err != nil {
		writeError(w, http.StatusNotFound, "节点未注册")
		return
	}
	version := strings.TrimSpace(jsonStr(body["version"]))
	if err := h.vpnStore().TouchNode(r.Context(), node.ID, version); err != nil {
		writeError(w, http.StatusInternalServerError, "心跳写入失败")
		return
	}

	settings, _ := h.vpnStore().LoadSettings(r.Context())
	transport := strings.TrimSpace(jsonStr(body["transport"]))
	if transport == "" {
		transport = string(settings.ManagementTransport)
	}
	state := strings.TrimSpace(jsonStr(body["state"]))
	if state == "" {
		state = "up"
	}
	rtt := int64(0)
	if v, ok := body["rtt_ms"].(float64); ok {
		rtt = int64(v)
	}
	_, _ = h.db.ExecContext(r.Context(),
		`INSERT INTO vpn_node_links (node_id, transport, state, rtt_ms, last_seen)
		 VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE transport = VALUES(transport), state = VALUES(state),
		   rtt_ms = VALUES(rtt_ms), last_seen = VALUES(last_seen)`,
		node.ID, transport, state, rtt, time.Now().UnixMilli())

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                   true,
		"management_transport": settings.ManagementTransport,
		"masque_fallback":      settings.MasqueFallback,
	})
}


