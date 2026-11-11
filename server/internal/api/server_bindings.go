package api

import (
	"net/http"
	"strconv"
)

// serverBindings 汇总一台服务器当前绑定/挂载了哪些资源。
//
// 用途：删除服务器前的确认弹窗要把「会被一起解绑的东西」列出来，
// 而不是只弹一句「删除后 Agent 与节点配置都会解除绑定」。
// 之前在管理端删服务器，用户并不知道删掉后 VPN 节点与它的用户授权
// 也会一起消失，属于破坏性操作缺少告知。
type serverBindingItem struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// GET /api/servers/{id}/bindings
func (h *handlers) serverBindings(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		h.notMigrated(w, r)
		return
	}

	ctx := r.Context()
	out := map[string]any{
		"vpn_nodes":    []serverBindingItem{},
		"singbox":      []serverBindingItem{},
		"counts":       map[string]int64{},
		"vpn_grants":   int64(0),
		"vpn_sessions": int64(0),
		"node_configs": int64(0),
	}

	// VPN 节点（以及它带走的用户授权数）
	rows, err := h.db.QueryContext(ctx,
		`SELECT n.id, n.name, COALESCE(n.endpoint, ''),
		        (SELECT COUNT(*) FROM vpn_node_grants g WHERE g.node_id = n.id)
		   FROM vpn_nodes n WHERE n.server_id = ? ORDER BY n.id`, id)
	if err == nil {
		defer rows.Close()
		var nodes []serverBindingItem
		var grants int64
		for rows.Next() {
			var it serverBindingItem
			var endpoint string
			var gcount int64
			if err := rows.Scan(&it.ID, &it.Name, &endpoint, &gcount); err == nil {
				it.Detail = endpoint
				if gcount > 0 {
					it.Detail += "（含 " + strconv.FormatInt(gcount, 10) + " 条用户授权）"
				}
				grants += gcount
				nodes = append(nodes, it)
			}
		}
		out["vpn_nodes"] = nodes
		out["vpn_grants"] = grants
	}

	// sing-box 节点绑定
	if rows2, err2 := h.db.QueryContext(ctx,
		`SELECT id, name FROM singbox_nodes WHERE server_id = ? ORDER BY id`, id); err2 == nil {
		defer rows2.Close()
		var items []serverBindingItem
		for rows2.Next() {
			var it serverBindingItem
			if err := rows2.Scan(&it.ID, &it.Name); err == nil {
				items = append(items, it)
			}
		}
		out["singbox"] = items
	}

	// 其余附属数据计数（仅用于告知）
	for key, q := range map[string]string{
		"vpn_sessions": `SELECT COUNT(*) FROM vpn_sessions s
		                   JOIN vpn_nodes n ON n.id = s.node_id WHERE n.server_id = ?`,
		"node_configs": `SELECT COUNT(*) FROM node_configs WHERE server_id = ?`,
	} {
		var n int64
		if err := h.db.QueryRowContext(ctx, q, id).Scan(&n); err == nil {
			out[key] = n
		}
	}

	writeJSON(w, http.StatusOK, out)
}
