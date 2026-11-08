package api

import (
	"net/http"
)

// POST /api/admin/vpn/mesh/batch — 批量检查组网 / 加入组网。
func (h *handlers) vpnAdminMeshBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ServerIDs []int64 `json:"server_ids"`
		Action    string  `json:"action"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	action := body.Action
	if action == "" {
		action = "check"
	}
	type result struct {
		ServerID int64  `json:"server_id"`
		Joined   bool   `json:"joined"`
		MeshIPv4 string `json:"mesh_ip4"`
		State    string `json:"state"`
		Queued   bool   `json:"queued"`
		Error    string `json:"error,omitempty"`
	}
	out := make([]result, 0, len(body.ServerIDs))
	for _, id := range body.ServerIDs {
		var res result
		res.ServerID = id
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT mesh_ip4, state FROM vpn_mesh_nodes WHERE server_id = ?`, id).
			Scan(&res.MeshIPv4, &res.State)
		res.Joined = res.MeshIPv4 != ""
		if action == "join" && !res.Joined {
			srv, err := h.fetchAgentServer(r.Context(), id)
			if err != nil || srv == nil {
				res.Error = "服务器 Agent 不存在"
			} else {
				h.autoMeshTask(r.Context(), srv)
				res.Queued = true
				res.State = "pending"
			}
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "results": out})
}
