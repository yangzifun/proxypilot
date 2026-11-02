package api

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

// GET /api/stats?days=30&latest=10
func (h *handlers) statsOverview(w http.ResponseWriter, r *http.Request) {
	days := atoiDefault(r.URL.Query().Get("days"), 30)
	if days < 1 {
		days = 30
	}
	if days > 3650 {
		days = 3650
	}
	latest := atoiDefault(r.URL.Query().Get("latest"), 10)
	if latest < 1 {
		latest = 10
	}
	if latest > 50 {
		latest = 50
	}

	var uuids, templates int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(DISTINCT uuid) FROM configs`).Scan(&uuids); err != nil {
		h.log.Error("统计 UUID 失败", "err", err)
	}
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM config_templates`).Scan(&templates); err != nil {
		h.log.Error("统计模板失败", "err", err)
	}

	access := h.accessStats(r.Context(), days, latest)
	writeJSON(w, http.StatusOK, map[string]any{
		"uuids":        uuids,
		"templates":    templates,
		"access_stats": access,
	})
}

// accessStats 复刻 getAccessStatsSummary 的返回结构。
// created_at 在 MySQL 里是 ISO 字符串，用 LEFT(...,10) 取日期，避免 DATE() 解析差异。
func (h *handlers) accessStats(ctx context.Context, days, latestLimit int) map[string]any {
	fail := func(msg string) map[string]any {
		return map[string]any{"success": false, "error": msg}
	}

	var (
		total, uniqueUUIDs, subTotal, apiGenTotal int64
	)
	if err := h.db.QueryRowContext(ctx, `SELECT
	      COUNT(*),
	      COUNT(DISTINCT uuid),
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0),
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0)
	    FROM config_access_logs`).
		Scan(&total, &uniqueUUIDs, &subTotal, &apiGenTotal); err != nil {
		return fail(err.Error())
	}

	today := time.Now().UTC().Format("2006-01-02")
	var todayTotal, todaySub, todayAPI int64
	if err := h.db.QueryRowContext(ctx, `SELECT
	      COUNT(*),
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0),
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0)
	    FROM config_access_logs WHERE LEFT(created_at,10) = ?`, today).
		Scan(&todayTotal, &todaySub, &todayAPI); err != nil {
		return fail(err.Error())
	}

	start := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	dailyRows, err := h.db.QueryContext(ctx, `SELECT LEFT(created_at,10) AS day,
	      COUNT(*) AS total,
	      COALESCE(SUM(CASE WHEN query_type = 'subscription' THEN 1 ELSE 0 END),0) AS subscription,
	      COALESCE(SUM(CASE WHEN query_type = 'api-generation' THEN 1 ELSE 0 END),0) AS api_generation,
	      COUNT(DISTINCT uuid) AS unique_uuids
	    FROM config_access_logs
	    WHERE LEFT(created_at,10) >= ?
	    GROUP BY day ORDER BY day DESC`, start)
	if err != nil {
		return fail(err.Error())
	}
	daily := []map[string]any{}
	for dailyRows.Next() {
		var (
			day                                 string
			dayTotal, daySub, dayAPI, dayUnique int64
		)
		if dailyRows.Scan(&day, &dayTotal, &daySub, &dayAPI, &dayUnique) == nil {
			daily = append(daily, map[string]any{
				"date": day, "total": dayTotal, "subscription": daySub,
				"api_generation": dayAPI, "unique_uuids": dayUnique,
			})
		}
	}
	dailyRows.Close()

	popularRows, err := h.db.QueryContext(ctx, `SELECT l.uuid,
	      COUNT(*) AS access_count,
	      COALESCE(SUM(CASE WHEN l.query_type = 'subscription' THEN 1 ELSE 0 END),0) AS subscription_count,
	      COALESCE(SUM(CASE WHEN l.query_type = 'api-generation' THEN 1 ELSE 0 END),0) AS apigen_count,
	      MAX(ui.owner) AS owner
	    FROM config_access_logs l
	    LEFT JOIN uuid_info ui ON ui.uuid = l.uuid
	    GROUP BY l.uuid ORDER BY access_count DESC LIMIT 15`)
	if err != nil {
		return fail(err.Error())
	}
	popular := []map[string]any{}
	for popularRows.Next() {
		var (
			uuid      string
			count     int64
			sub, apig int64
			owner     sql.NullString
		)
		if popularRows.Scan(&uuid, &count, &sub, &apig, &owner) == nil {
			var ownerVal any
			if owner.Valid {
				ownerVal = owner.String
			}
			popular = append(popular, map[string]any{
				"uuid": uuid, "access_count": count, "subscription_count": sub,
				"apigen_count": apig, "owner": ownerVal,
			})
		}
	}
	popularRows.Close()

	latestRows, err := h.db.QueryContext(ctx, `SELECT l.uuid, l.query_type, l.created_at, ui.owner
	    FROM config_access_logs l
	    LEFT JOIN uuid_info ui ON ui.uuid = l.uuid
	    ORDER BY l.created_at DESC LIMIT ?`, latestLimit)
	if err != nil {
		return fail(err.Error())
	}
	latestLogs := []map[string]any{}
	for latestRows.Next() {
		var (
			uuid, queryType, createdAt string
			owner                      sql.NullString
		)
		if latestRows.Scan(&uuid, &queryType, &createdAt, &owner) == nil {
			var ownerVal any
			if owner.Valid {
				ownerVal = owner.String
			}
			latestLogs = append(latestLogs, map[string]any{
				"uuid": uuid, "query_type": queryType, "created_at": createdAt, "owner": ownerVal,
			})
		}
	}
	latestRows.Close()

	return map[string]any{
		"success":                 true,
		"total_requests":          total,
		"unique_uuids":            uniqueUUIDs,
		"subscription_requests":   subTotal,
		"api_generation_requests": apiGenTotal,
		"today_total":             todayTotal,
		"today_subscription":      todaySub,
		"today_apigen":            todayAPI,
		"daily_stats":             daily,
		"popular_uuids":           popular,
		"latest_logs":             latestLogs,
	}
}
