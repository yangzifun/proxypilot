package api

import (
	"net/http"
	"strconv"
	"time"
)

// statsTrafficRate 返回全站实时速率（每分钟聚合），用于用户流量页速率曲线。
// GET /api/stats/traffic/rate?minutes=60
func (h *handlers) statsTrafficRate(w http.ResponseWriter, r *http.Request) {
	minutes := 60
	if v, err := strconv.Atoi(r.URL.Query().Get("minutes")); err == nil && v > 0 && v <= 1440 {
		minutes = v
	}
	since := time.Now().Add(-time.Duration(minutes) * time.Minute).UnixMilli()
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT FLOOR(created_at/60000)*60000 AS bucket,
		        COALESCE(SUM(net_rx_rate),0) AS down,
		        COALESCE(SUM(net_tx_rate),0) AS up
		 FROM server_metrics
		 WHERE created_at >= ?
		 GROUP BY bucket ORDER BY bucket ASC`, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		ts := mapInt(row, "bucket", 0)
		out = append(out, map[string]any{
			"label": time.UnixMilli(ts).Format("15:04"),
			"down":  mapInt(row, "down", 0),
			"up":    mapInt(row, "up", 0),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "minutes": minutes, "data": out})
}
