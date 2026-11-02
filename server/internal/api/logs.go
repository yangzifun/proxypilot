package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

const (
	logCleanupBatch      = 500
	logCleanupMaxBatches = 20
	logCleanupMaxDays    = 3650
)

var (
	dateOnlyRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dateISORe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{3})?Z$`)
)

// GET /api/logs/summary（超管）
func (h *handlers) logsSummary(w http.ResponseWriter, r *http.Request) {
	var total int64
	var oldest, newest sql.NullString
	var subscription, apiGeneration int64

	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*), MIN(created_at), MAX(created_at),
		        COALESCE(SUM(CASE WHEN query_type='subscription' THEN 1 ELSE 0 END),0),
		        COALESCE(SUM(CASE WHEN query_type='api-generation' THEN 1 ELSE 0 END),0)
		   FROM config_access_logs`).
		Scan(&total, &oldest, &newest, &subscription, &apiGeneration); err != nil {
		h.log.Error("读取日志统计失败", "err", err)
		writeError(w, http.StatusInternalServerError, "读取日志统计失败")
		return
	}

	olderThan := map[string]int64{}
	for _, days := range []int{30, 90, 180, 365} {
		cutoff := cutoffForDays(days)
		var c int64
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM config_access_logs WHERE created_at < ?`, cutoff).Scan(&c)
		olderThan[strconv.Itoa(days)+"d"] = c
	}

	var oldestVal, newestVal any
	if oldest.Valid {
		oldestVal = oldest.String
	}
	if newest.Valid {
		newestVal = newest.String
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"total":          total,
		"oldest":         oldestVal,
		"newest":         newestVal,
		"subscription":   subscription,
		"api_generation": apiGeneration,
		"older_than":     olderThan,
	})
}

// POST /api/logs/cleanup（超管，需二次输入密码）
func (h *handlers) logsCleanup(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsFrom(r)

	var body struct {
		Days     any    `json:"days"`
		Before   string `json:"before"`
		DryRun   bool   `json:"dry_run"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &body); err != nil && !strings.Contains(err.Error(), "EOF") {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}

	cutoff, errMsg := resolveLogCutoff(body.Days, body.Before)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}

	var total, wouldDelete int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM config_access_logs`).Scan(&total)
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM config_access_logs WHERE created_at < ?`, cutoff).Scan(&wouldDelete)

	if body.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "dry_run": true, "cutoff": cutoff,
			"total": total, "would_delete": wouldDelete,
		})
		return
	}
	if strings.TrimSpace(body.Password) == "" {
		writeError(w, http.StatusBadRequest, "请输入当前登录密码")
		return
	}

	var storedHash string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT password_hash FROM admin_users WHERE username = ? LIMIT 1`, claims.Sub).Scan(&storedHash)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}
	if ok, _ := auth.VerifyPassword(storedHash, body.Password); !ok {
		writeError(w, http.StatusUnauthorized, "密码错误")
		return
	}

	deleted, remaining, batches, capped := h.deleteLogsBefore(r.Context(), cutoff)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"cutoff":       cutoff,
		"total_before": total,
		"would_delete": wouldDelete,
		"deleted":      deleted,
		"remaining":    remaining,
		"batches":      batches,
		"capped":       capped,
	})
}

func (h *handlers) deleteLogsBefore(ctx context.Context, cutoff string) (deleted, remaining, batches int64, capped bool) {
	for i := 0; i < logCleanupMaxBatches; i++ {
		res, err := h.db.ExecContext(ctx,
			`DELETE FROM config_access_logs WHERE created_at < ? LIMIT ?`, cutoff, logCleanupBatch)
		batches++
		if err != nil {
			h.log.Error("清理访问日志失败", "err", err)
			break
		}
		n, _ := res.RowsAffected()
		deleted += n
		if n < logCleanupBatch {
			break
		}
		if i == logCleanupMaxBatches-1 {
			capped = true
		}
	}
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_access_logs WHERE created_at < ?`, cutoff).Scan(&remaining)
	return deleted, remaining, batches, capped
}

func cutoffForDays(days int) string {
	return time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02T15:04:05Z")
}

// resolveLogCutoff 复刻 Worker：days 优先，其次 before（日期或完整 ISO）。
func resolveLogCutoff(days any, before string) (string, string) {
	if days != nil {
		raw := strings.TrimSpace(toString(days))
		if raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > logCleanupMaxDays {
				return "", "保留期限天数超出范围（1-3650）"
			}
			return cutoffForDays(n), ""
		}
	}
	bf := strings.TrimSpace(before)
	if bf != "" {
		var iso string
		switch {
		case dateOnlyRe.MatchString(bf):
			iso = bf + "T00:00:00Z"
		case dateISORe.MatchString(bf):
			iso = bf
		default:
			return "", "截止日期格式无效（应为 YYYY-MM-DD）"
		}
		ts, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return "", "截止日期格式无效（应为 YYYY-MM-DD）"
		}
		if ts.After(time.Now()) {
			return "", "截止日期不能晚于当前时间"
		}
		return iso, ""
	}
	return "", "请提供 days 或 before"
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}
