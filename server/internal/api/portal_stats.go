package api

import (
	"net/http"
	"strings"
	"time"
)

// GET /manage/stats/{uuid}?days=30 —— 用户端「访问统计」JSON。
// 之前这个路径被 SPA 页面兜底接管，前端拿到 HTML 后只能显示 0。
func (h *handlers) manageStats(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !h.requireManage(w, r, uuid) {
		return
	}
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	if days < 1 {
		days = 30
	}
	if days > 3650 {
		days = 3650
	}
	ctx := r.Context()

	var total, subscription, apigen, uniqueIPs int64
	if err := h.db.QueryRowContext(ctx, `SELECT
	      COUNT(*),
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0),
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0),
	      COUNT(DISTINCT client_ip)
	    FROM config_access_logs WHERE uuid = ?`, uuid).
		Scan(&total, &subscription, &apigen, &uniqueIPs); err != nil {
		writeError(w, http.StatusInternalServerError, "统计读取失败")
		return
	}

	today := time.Now().UTC().Format("2006-01-02")
	var todayTotal, todaySub, todayAPI int64
	_ = h.db.QueryRowContext(ctx, `SELECT
	      COUNT(*),
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0),
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0)
	    FROM config_access_logs WHERE uuid = ? AND LEFT(created_at,10) = ?`, uuid, today).
		Scan(&todayTotal, &todaySub, &todayAPI)

	start := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	rows, err := h.db.QueryContext(ctx, `SELECT LEFT(created_at,10) AS day,
	      COUNT(*) AS total,
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0) AS subscription,
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0) AS api_generation,
	      COUNT(DISTINCT client_ip) AS unique_ips
	    FROM config_access_logs
	    WHERE uuid = ? AND LEFT(created_at,10) >= ?
	    GROUP BY day ORDER BY day ASC`, uuid, start)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "趋势统计读取失败")
		return
	}
	daily := []map[string]any{}
	for rows.Next() {
		var (
			day                                 string
			dayTotal, daySub, dayAPI, dayUnique int64
		)
		if rows.Scan(&day, &dayTotal, &daySub, &dayAPI, &dayUnique) == nil {
			daily = append(daily, map[string]any{
				"date": day, "total": dayTotal, "subscription": daySub,
				"api_generation": dayAPI, "unique_ips": dayUnique,
			})
		}
	}
	rows.Close()

	recent := []map[string]any{}
	rows, err = h.db.QueryContext(ctx, `SELECT query_type, client_ip, user_agent, created_at
	    FROM config_access_logs WHERE uuid = ?
	    ORDER BY created_at DESC, id DESC LIMIT 20`, uuid)
	if err == nil {
		for rows.Next() {
			var queryType, ip, ua, createdAt string
			if rows.Scan(&queryType, &ip, &ua, &createdAt) == nil {
				recent = append(recent, map[string]any{
					"query_type": queryType, "client_ip": ip, "user_agent": ua, "created_at": createdAt,
				})
			}
		}
		rows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":                true,
		"uuid":                   uuid,
		"total_access":           total,
		"unique_ips":             uniqueIPs,
		"subscription_count":     subscription,
		"apigen_count":           apigen,
		"today_total":            todayTotal,
		"today_subscription":     todaySub,
		"today_apigen":           todayAPI,
		"daily_stats":            daily,
		"recent_logs":            recent,
		"days":                   days,
	})
}
