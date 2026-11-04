package api

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// GET /api/traffic/nodes
// 汇总「按节点」的流量：sing-box 节点（user_traffic_hourly.node_id）与 mihomo 绑定（按 UUID 或节点聚合）。
func (h *handlers) trafficNodes(w http.ResponseWriter, r *http.Request) {
	nowT := time.Now()
	dayStart := time.Date(nowT.Year(), nowT.Month(), nowT.Day(), 0, 0, 0, 0, nowT.Location()).UnixMilli()
	weekStart := dayStart - 6*86400000
	onlineSince := nowT.Add(-5 * time.Minute).UnixMilli()

	singbox, err := h.trafficForSingboxNodes(r.Context(), dayStart, weekStart, onlineSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点流量失败")
		return
	}
	mihomo, err := h.trafficForMihomoBindings(r.Context(), dayStart, weekStart, onlineSince)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 mihomo 流量失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"singbox": singbox, "mihomo": mihomo},
	})
}

func (h *handlers) trafficForSingboxNodes(ctx context.Context, dayStart, weekStart, onlineSince int64) ([]map[string]any, error) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT n.id, n.name, n.server_id, COALESCE(s.name,'') AS server_name,
		        COALESCE(SUM(t.up + t.down), 0) AS total,
		        COALESCE(SUM(CASE WHEN t.hour_ts >= ? THEN t.up + t.down ELSE 0 END), 0) AS today,
		        COALESCE(SUM(CASE WHEN t.hour_ts >= ? THEN t.up + t.down ELSE 0 END), 0) AS week,
		        COUNT(DISTINCT CASE WHEN t.hour_ts >= ? THEN t.uuid END) AS online
		   FROM singbox_nodes n
		   LEFT JOIN servers s ON s.id = n.server_id
		   LEFT JOIN user_traffic_hourly t ON t.node_id = n.id
		  GROUP BY n.id, n.name, n.server_id, s.name
		  ORDER BY total DESC, n.id ASC`,
		dayStart, weekStart, onlineSince)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		row["kind"] = "singbox"
		row["online"] = mapInt(row, "online", 0)
		row["uuid_count"] = mapInt(row, "online", 0)
	}
	return rows, nil
}

func (h *handlers) trafficForMihomoBindings(ctx context.Context, dayStart, weekStart, onlineSince int64) ([]map[string]any, error) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT n.id, n.name, n.server_id, COALESCE(n.uuid_list,'[]') AS uuid_list,
		        COALESCE(s.name,'') AS server_name, COALESCE(t.name,'') AS template_name
		   FROM mihomo_nodes n
		   LEFT JOIN servers s ON s.id = n.server_id
		   LEFT JOIN mihomo_templates t ON t.id = n.template_id
		  ORDER BY n.id DESC`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		var today, week, total int64
		var online int
		if len(list) > 0 {
			ph := strings.TrimSuffix(strings.Repeat("?,", len(list)), ",")
			args := make([]any, 0, len(list))
			for _, u := range list {
				args = append(args, u)
			}
			_ = h.db.QueryRowContext(ctx,
				`SELECT COALESCE(SUM(up+down),0),
				        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0),
				        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0)
				   FROM user_traffic_hourly WHERE uuid IN (`+ph+`)`,
				append([]any{dayStart, weekStart}, args...)...).Scan(&total, &today, &week)
			_ = h.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM user_traffic WHERE last_active_at >= ? AND uuid IN (`+ph+`)`,
				append([]any{onlineSince}, args...)...).Scan(&online)
		} else {
			var nodeID int64
			if h.db.QueryRowContext(ctx,
				`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`,
				mapInt(row, "server_id", 0)).Scan(&nodeID) == nil && nodeID > 0 {
				_ = h.db.QueryRowContext(ctx,
					`SELECT COALESCE(SUM(up+down),0),
					        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0),
					        COALESCE(SUM(CASE WHEN hour_ts >= ? THEN up+down ELSE 0 END),0)
					   FROM user_traffic_hourly WHERE node_id = ?`,
					dayStart, weekStart, nodeID).Scan(&total, &today, &week)
				_ = h.db.QueryRowContext(ctx,
					`SELECT COUNT(DISTINCT uuid) FROM user_traffic_hourly WHERE node_id = ? AND hour_ts >= ?`,
					nodeID, onlineSince).Scan(&online)
			}
		}
		row["kind"] = "mihomo"
		row["uuid_list"] = list
		row["uuid_count"] = len(list)
		row["traffic_today"] = today
		row["traffic_week"] = week
		row["traffic_total"] = total
		row["online"] = online
	}
	return rows, nil
}
