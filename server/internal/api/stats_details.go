package api

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"
)

type accessLogRow struct {
	UUID      string `json:"uuid"`
	QueryType string `json:"query_type"`
	ClientIP  string `json:"client_ip"`
	UserAgent string `json:"user_agent"`
	CreatedAt int64  `json:"created_at"`
}

func (h *handlers) uuidAccessDetails(ctx context.Context, uuid string, startDate, endDate string) map[string]any {
	uuid = strings.TrimSpace(uuid)
	var (
		total   int64
		firstAt sql.NullInt64
		lastAt  sql.NullInt64
		startMS int64
		endMS   int64
	)
	if t, err := time.Parse("2006-01-02", startDate); err == nil {
		startMS = t.UnixMilli()
	}
	if t, err := time.Parse("2006-01-02", endDate); err == nil {
		endMS = t.Add(24*time.Hour - time.Millisecond).UnixMilli()
	}

	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(created_at), MAX(created_at) FROM config_access_logs WHERE uuid = ?`, uuid).
		Scan(&total, &firstAt, &lastAt)

	query := `SELECT COALESCE(uuid,''), COALESCE(query_type,''), COALESCE(client_ip,''),
	                 COALESCE(user_agent,''), COALESCE(created_at,0)
	          FROM config_access_logs WHERE uuid = ?`
	args := []any{uuid}
	if startMS > 0 {
		query += ` AND created_at >= ?`
		args = append(args, startMS)
	}
	if endMS > 0 {
		query += ` AND created_at <= ?`
		args = append(args, endMS)
	}
	query += ` ORDER BY created_at DESC LIMIT 100`

	logs := []accessLogRow{}
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r accessLogRow
			if rows.Scan(&r.UUID, &r.QueryType, &r.ClientIP, &r.UserAgent, &r.CreatedAt) == nil {
				logs = append(logs, r)
			}
		}
	}
	out := map[string]any{
		"success": true, "uuid": uuid, "total_access": total, "access_logs": logs,
	}
	if firstAt.Valid {
		out["first_access"] = firstAt.Int64
	}
	if lastAt.Valid {
		out["last_access"] = lastAt.Int64
	}
	return out
}

// GET /api/stats/uuid-details?uuid=&start_date=&end_date=
func (h *handlers) statsUUIDDetails(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, h.uuidAccessDetails(r.Context(), q.Get("uuid"), q.Get("start_date"), q.Get("end_date")))
}

// GET /api/uuid-configs/stats?uuid=&days=
func (h *handlers) uuidConfigsStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	writeJSON(w, http.StatusOK, h.uuidAccessDetails(r.Context(), q.Get("uuid"), "", ""))
}
