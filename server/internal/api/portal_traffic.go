package api

import (
	"net/http"
	"strings"
)

// GET /api/portal/traffic?uuid=
// 用户中心：当前登录邮箱名下 UUID 的流量用量（今日 / 近 7 天 / 累计）。
func (h *handlers) portalTraffic(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	uuid := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("uuid")))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	var owned int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_info WHERE uuid = ? AND LOWER(email) = ?`, uuid, email).Scan(&owned)
	if owned == 0 {
		writeError(w, http.StatusForbidden, "无权查看该 UUID 的流量")
		return
	}

	ctx := r.Context()
	now := nowMillis()
	todayStart := now - now%86_400_000
	weekStart := now - 7*24*3_600_000

	type pair struct{ up, down int64 }
	queryPair := func(where string, args ...any) pair {
		var p pair
		_ = h.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(up),0), COALESCE(SUM(down),0) FROM user_traffic_hourly WHERE uuid = ? AND `+where,
			append([]any{uuid}, args...)...).Scan(&p.up, &p.down)
		return p
	}
	today := queryPair("hour_ts >= ?", todayStart)
	week := queryPair("hour_ts >= ?", weekStart)

	var upTotal, downTotal, lastActive int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(up_total,0), COALESCE(down_total,0), COALESCE(last_active_at,0)
		   FROM user_traffic WHERE uuid = ? LIMIT 1`, uuid).Scan(&upTotal, &downTotal, &lastActive)

	// 近 7 天按天汇总，供趋势图使用
	series := []map[string]any{}
	if rows, err := h.db.QueryContext(r.Context(),
		`SELECT FLOOR(hour_ts / 86400000) * 86400000 AS day, COALESCE(SUM(up),0), COALESCE(SUM(down),0)
		   FROM user_traffic_hourly WHERE uuid = ? AND hour_ts >= ?
		  GROUP BY day ORDER BY day ASC`, uuid, weekStart); err == nil {
		for rows.Next() {
			var ts, up, down int64
			if rows.Scan(&ts, &up, &down) == nil {
				series = append(series, map[string]any{"ts": ts, "up": up, "down": down})
			}
		}
		_ = rows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "uuid": uuid,
		"today":          map[string]any{"up": today.up, "down": today.down, "total": today.up + today.down},
		"week":           map[string]any{"up": week.up, "down": week.down, "total": week.up + week.down},
		"total":          map[string]any{"up": upTotal, "down": downTotal, "total": upTotal + downTotal},
		"last_active_at": lastActive,
		"active":         lastActive > 0 && now-lastActive <= statsActiveWindowMs,
		"series":         series,
	})
}
