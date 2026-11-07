package api

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// GET /api/stats/traffic/flow?hours=24
// 返回「多用户 → 多服务器」的流量流向聚合，用于总览流向图。
func (h *handlers) statsTrafficFlow(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("hours")), 10, 64)
	if hours <= 0 || hours > 24*30 {
		hours = 24
	}
	since := (nowMillis()/3600000 - hours + 1) * 3600000

	rows, err := queryRowsAsMaps(r.Context(), h.db, `
		SELECT ut.uuid AS uuid,
		       COALESCE(ui.owner,'') AS owner, COALESCE(ui.email,'') AS email,
		       ut.node_id AS node_id,
		       COALESCE(n.name,'') AS node_name,
		       COALESCE(s.name,'') AS server_name,
		       COALESCE(SUM(ut.up),0) AS up, COALESCE(SUM(ut.down),0) AS down
		  FROM user_traffic_hourly ut
		  LEFT JOIN uuid_info ui ON ui.uuid = ut.uuid
		  LEFT JOIN singbox_nodes n ON n.id = ut.node_id
		  LEFT JOIN servers s ON s.id = n.server_id
		 WHERE ut.hour_ts >= ?
		 GROUP BY ut.uuid, ui.owner, ui.email, ut.node_id, n.name, s.name
		 ORDER BY (COALESCE(SUM(ut.up),0) + COALESCE(SUM(ut.down),0)) DESC
		 LIMIT 400`, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询流量流向失败")
		return
	}

	// 最近活跃（2 分钟内）的用户集合
	active := map[string]bool{}
	if rr, err := h.db.QueryContext(r.Context(),
		`SELECT DISTINCT uuid FROM user_node_recent WHERE last_ts >= ?`, nowMillis()-statsActiveWindowMs); err == nil {
		for rr.Next() {
			var u string
			if rr.Scan(&u) == nil {
				active[u] = true
			}
		}
		rr.Close()
	}

	type agg struct {
		key   string
		label string
		total int64
	}
	userTotals := map[string]*agg{}
	nodeTotals := map[string]*agg{}
	edgeTotals := map[string]int64{}

	type edge struct {
		UUID     string `json:"uuid"`
		NodeID   int64  `json:"node_id"`
		Total    int64  `json:"total"`
		Up       int64  `json:"up"`
		Down     int64  `json:"down"`
		Active   bool   `json:"active"`
	}
	edges := make([]edge, 0, len(rows))

	for _, row := range rows {
		uuid := mapStr(row, "uuid", "")
		nodeID := mapInt(row, "node_id", 0)
		up := mapInt(row, "up", 0)
		down := mapInt(row, "down", 0)
		total := up + down
		if uuid == "" || total <= 0 {
			continue
		}
		uKey := uuid
		if _, ok := userTotals[uKey]; !ok {
			label := mapStr(row, "owner", "")
			if label == "" {
				label = mapStr(row, "email", "")
			}
			if label == "" {
				label = uuid[:8]
			}
			userTotals[uKey] = &agg{key: uKey, label: label}
		}
		userTotals[uKey].total += total

		nKey := itoa64(nodeID)
		if nodeID > 0 {
			if _, ok := nodeTotals[nKey]; !ok {
				name := mapStr(row, "node_name", "")
				if name == "" {
					name = mapStr(row, "server_name", "")
				}
				if name == "" {
					name = "#" + nKey
				}
				nodeTotals[nKey] = &agg{key: nKey, label: name}
			}
			nodeTotals[nKey].total += total
			edgeTotals[uuid+"|"+nKey] += total
		}
		edges = append(edges, edge{UUID: uuid, NodeID: nodeID, Total: total, Up: up, Down: down, Active: active[uuid]})
	}

	pickTop := func(m map[string]*agg, n int) []*agg {
		list := make([]*agg, 0, len(m))
		for _, v := range m {
			list = append(list, v)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].total > list[j].total })
		if len(list) > n {
			list = list[:n]
		}
		return list
	}

	topUsers := pickTop(userTotals, 8)
	topNodes := pickTop(nodeTotals, 8)
	keepUser := map[string]bool{}
	keepNode := map[string]bool{}
	for _, u := range topUsers {
		keepUser[u.key] = true
	}
	for _, n := range topNodes {
		keepNode[n.key] = true
	}

	// 只保留两端都在 Top 列表里的连线，并按 (user,node) 合并
	merged := map[string]*edge{}
	for _, e := range edges {
		if !keepUser[e.UUID] || !keepNode[itoa64(e.NodeID)] {
			continue
		}
		k := e.UUID + "|" + itoa64(e.NodeID)
		if cur, ok := merged[k]; ok {
			cur.Up += e.Up
			cur.Down += e.Down
			cur.Total += e.Total
			cur.Active = cur.Active || e.Active
			continue
		}
		cp := e
		merged[k] = &cp
	}

	outEdges := make([]edge, 0, len(merged))
	for _, e := range merged {
		outEdges = append(outEdges, *e)
	}
	sort.Slice(outEdges, func(i, j int) bool { return outEdges[i].Total > outEdges[j].Total })

	userOut := make([]map[string]any, 0, len(topUsers))
	for _, u := range topUsers {
		userOut = append(userOut, map[string]any{
			"uuid": u.key, "label": u.label, "total": u.total, "active": active[u.key],
		})
	}
	nodeOut := make([]map[string]any, 0, len(topNodes))
	for _, n := range topNodes {
		nodeOut = append(nodeOut, map[string]any{
			"node_id": n.key, "label": n.label, "total": n.total,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"hours":   hours,
		"users":   userOut,
		"nodes":   nodeOut,
		"edges":   outEdges,
	})
}
