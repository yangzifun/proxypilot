package api

import (
	"net/http"
	"strconv"
	"time"
)

// statsTrafficSeries 返回按天的流量汇总，可按 uuid 过滤到单个用户。
// GET /api/stats/traffic/series?days=14&uuid=xxx
func (h *handlers) statsTrafficSeries(w http.ResponseWriter, r *http.Request) {
	days := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 90 {
		days = v
	}
	uuid := r.URL.Query().Get("uuid")
	since := time.Now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour).UnixMilli()
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT hour_ts, COALESCE(SUM(up),0) AS up, COALESCE(SUM(down),0) AS down
		 FROM user_traffic_hourly
		 WHERE hour_ts >= ? AND (? = '' OR uuid = ?)
		 GROUP BY hour_ts ORDER BY hour_ts ASC`, since, uuid, uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type bucket struct {
		up, down int64
	}
	byDay := map[string]*bucket{}
	for _, row := range rows {
		ts := mapInt(row, "hour_ts", 0)
		day := time.Unix(ts/1000, 0).Format("2006-01-02")
		b := byDay[day]
		if b == nil {
			b = &bucket{}
			byDay[day] = b
		}
		b.up += mapInt(row, "up", 0)
		b.down += mapInt(row, "down", 0)
	}
	out := make([]map[string]any, 0, days)
	today := time.Now().Truncate(24 * time.Hour)
	for i := days - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i)
		key := d.Format("2006-01-02")
		b := byDay[key]
		var up, down int64
		if b != nil {
			up, down = b.up, b.down
		}
		out = append(out, map[string]any{
			"label": d.Format("01-02"),
			"day":   key,
			"up":    up,
			"down":  down,
			"total": up + down,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "days": days, "uuid": uuid, "data": out})
}
