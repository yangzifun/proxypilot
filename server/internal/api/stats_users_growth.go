package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// statsUsersGrowth 返回每日新增用户数（按 users.created_at 聚合）。
// GET /api/stats/users/growth?days=30
func (h *handlers) statsUsersGrowth(w http.ResponseWriter, r *http.Request) {
	days := 30
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 180 {
		days = v
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT DATE(FROM_UNIXTIME(created_at/1000)) AS day, COUNT(DISTINCT uuid) AS cnt
		 FROM configs
		 WHERE created_at >= ?
		 GROUP BY day ORDER BY day ASC`, time.Now().AddDate(0, 0, -days+1).UnixMilli())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	byDay := map[string]int64{}
	for _, row := range rows {
		byDay[fmt.Sprint(row["day"])] = mapInt(row, "cnt", 0)
	}
	out := make([]map[string]any, 0, days)
	today := time.Now().Truncate(24 * time.Hour)
	for i := days - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		out = append(out, map[string]any{"label": d.Format("01-02"), "day": key, "value": byDay[key]})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "days": days, "data": out})
}
