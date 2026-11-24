package api

import (
	"context"
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type clientErrorRecord struct {
	ID        int64  `json:"id"`
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	URL       string `json:"url"`
	Page      string `json:"page"`
	Mode      string `json:"mode"`
	UA        string `json:"ua"`
	CreatedAt int64  `json:"created_at"`
}

// storeClientError 尽力持久化上报内容；DB 未配置时直接跳过，错误由调用方忽略。
// 写入前再次执行与日志一致的截断/去敏，保证数据库不保存 query 参数。
func (h *handlers) storeClientError(ctx context.Context, p clientErrorPayload) error {
	if h.db == nil {
		return nil
	}
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO client_errors (message, stack, url, page, mode, ua, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		truncateRunes(strings.TrimSpace(p.Message), clientErrorMessage),
		truncateRunes(strings.TrimSpace(p.Stack), clientErrorStack),
		sanitizeClientErrorURL(p.URL),
		truncateRunes(strings.TrimSpace(p.Page), clientErrorPage),
		clientErrorModeText(p.Mode),
		truncateRunes(strings.TrimSpace(p.UA), clientErrorUA),
		nowMillis(),
	)
	return err
}

// GET /api/client-errors（authed，普通管理员可读）
func (h *handlers) clientErrorsList(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	qs := r.URL.Query()
	page := atoiDefault(qs.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	size := atoiDefault(qs.Get("size"), 20)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	mode := truncateRunes(strings.ToLower(strings.TrimSpace(qs.Get("mode"))), clientErrorMode)
	q := truncateRunes(strings.TrimSpace(qs.Get("q")), 100)
	from, fromOK := parseClientErrorMillis(qs.Get("from"))
	to, toOK := parseClientErrorMillis(qs.Get("to"))
	if (!fromOK && strings.TrimSpace(qs.Get("from")) != "") || (!toOK && strings.TrimSpace(qs.Get("to")) != "") {
		writeError(w, http.StatusBadRequest, "from/to 必须是毫秒时间戳")
		return
	}
	if from > 0 && to > 0 && from > to {
		writeError(w, http.StatusBadRequest, "from 不能大于 to")
		return
	}

	conds := []string{}
	args := []any{}
	if mode != "" {
		conds = append(conds, "mode = ?")
		args = append(args, mode)
	}
	if q != "" {
		conds = append(conds, "(LOCATE(?, message) > 0 OR LOCATE(?, page) > 0 OR LOCATE(?, url) > 0)")
		args = append(args, q, q, q)
	}
	if from > 0 {
		conds = append(conds, "created_at >= ?")
		args = append(args, from)
	}
	if to > 0 {
		conds = append(conds, "created_at <= ?")
		args = append(args, to)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM client_errors`+where, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "查询客户端错误失败")
		return
	}

	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, COALESCE(message,''), COALESCE(stack,''), COALESCE(url,''),
		        COALESCE(page,''), COALESCE(mode,''), COALESCE(ua,''), COALESCE(created_at,0)
		   FROM client_errors`+where+`
		  ORDER BY created_at DESC, id DESC
		  LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询客户端错误失败")
		return
	}
	defer rows.Close()

	data := []clientErrorRecord{}
	for rows.Next() {
		var rec clientErrorRecord
		if err := rows.Scan(&rec.ID, &rec.Message, &rec.Stack, &rec.URL, &rec.Page, &rec.Mode, &rec.UA, &rec.CreatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "解析客户端错误失败")
			return
		}
		data = append(data, rec)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   total,
		"page":    page,
		"size":    size,
		"data":    data,
	})
}

// parseClientErrorMillis 解析可选毫秒时间戳；空字符串返回 0,true。
func parseClientErrorMillis(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// GET /api/client-errors/stats?range=24h|7d|30d（authed，普通管理员可读）
func (h *handlers) clientErrorStats(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	rangeKey := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("range")))
	if rangeKey == "" {
		rangeKey = "7d"
	}
	var window, bucket int64
	granularity := "day"
	switch rangeKey {
	case "24h":
		window, bucket, granularity = 24*3600*1000, 3600*1000, "hour"
	case "7d":
		window, bucket = 7*24*3600*1000, 24*3600*1000
	case "30d":
		window, bucket = 30*24*3600*1000, 24*3600*1000
	default:
		writeError(w, http.StatusBadRequest, "range 只能是 24h / 7d / 30d")
		return
	}
	now := nowMillis()
	since := now - window

	var total, latest int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*), COALESCE(MAX(created_at),0) FROM client_errors WHERE created_at >= ?`, since).
		Scan(&total, &latest); err != nil {
		writeError(w, http.StatusInternalServerError, "统计失败")
		return
	}

	byMode := []map[string]any{}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT COALESCE(mode,''), COUNT(*) AS cnt FROM client_errors
		  WHERE created_at >= ? GROUP BY mode ORDER BY cnt DESC, mode ASC`, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "统计失败")
		return
	}
	for rows.Next() {
		var mode string
		var cnt int64
		if rows.Scan(&mode, &cnt) == nil {
			byMode = append(byMode, map[string]any{"mode": mode, "count": cnt})
		}
	}
	rows.Close()

	trend := []map[string]any{}
	rows, err = h.db.QueryContext(r.Context(),
		`SELECT CAST(FLOOR(created_at / ?) * ? AS SIGNED) AS bucket, COUNT(*) AS cnt FROM client_errors
		  WHERE created_at >= ? GROUP BY bucket ORDER BY bucket ASC`, bucket, bucket, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "统计失败")
		return
	}
	for rows.Next() {
		var b, cnt int64
		if rows.Scan(&b, &cnt) == nil {
			label := time.UnixMilli(b).UTC().Format("01-02 15:00")
			if granularity == "day" {
				label = time.UnixMilli(b).UTC().Format("2006-01-02")
			}
			trend = append(trend, map[string]any{"bucket": b, "label": label, "count": cnt})
		}
	}
	rows.Close()

	pickTop := func(column string, limit int) []map[string]any {
		out := []map[string]any{}
		q := `SELECT ` + column + `, COUNT(*) AS cnt FROM client_errors WHERE created_at >= ? AND ` + column + ` <> '' GROUP BY ` + column + ` ORDER BY cnt DESC, ` + column + ` ASC LIMIT ?`
		r, qerr := h.db.QueryContext(r.Context(), q, since, limit)
		if qerr != nil {
			return out
		}
		defer r.Close()
		for r.Next() {
			var val string
			var cnt int64
			if r.Scan(&val, &cnt) == nil {
				out = append(out, map[string]any{column: truncateRunes(val, 160), "count": cnt})
			}
		}
		return out
	}
	topMessages := pickTop("message", 10)
	topPages := pickTop("page", 10)

	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"range":        rangeKey,
		"since":        since,
		"total":        total,
		"by_mode":      byMode,
		"trend":        trend,
		"granularity":  granularity,
		"top_messages": topMessages,
		"top_pages":    topPages,
		"latest_at":    latest,
	})
}

// cleanupClientErrors 每日删除超过保留期的客户端错误；默认 30 天，范围 7–365。
func (h *handlers) cleanupClientErrors(ctx context.Context, now int64) {
	if h.db == nil {
		return
	}
	last := int64(intOrJS(h.getSetting(ctx, "client_error_cleanup_at", "0"), 0, 0, 1<<60))
	if now-last < 24*3600*1000 {
		return
	}
	days := int64(intOrJS(h.getSetting(ctx, "client_error_retention_days", "30"), 30, 7, 365))
	cutoff := now - days*24*3600*1000
	if _, err := h.db.ExecContext(ctx,
		`DELETE FROM client_errors WHERE created_at > 0 AND created_at < ? LIMIT 5000`, cutoff); err != nil {
		return
	}
	_ = h.putSetting(ctx, "client_error_cleanup_at", itoa64(now))
	h.invalidateSettings()
}

// clientErrorAdminFilter 解析批量删除/导出条件；至少需要 mode 或 before 之一。
func clientErrorAdminFilter(r *http.Request) (string, []any, error) {
	qs := r.URL.Query()
	mode := truncateRunes(strings.ToLower(strings.TrimSpace(qs.Get("mode"))), clientErrorMode)
	beforeRaw := strings.TrimSpace(qs.Get("before"))
	var before int64
	if beforeRaw != "" {
		n, err := strconv.ParseInt(beforeRaw, 10, 64)
		if err != nil || n <= 0 {
			return "", nil, errInvalidClientErrorBefore
		}
		before = n
	}
	if mode == "" && before == 0 {
		return "", nil, errClientErrorFilterRequired
	}
	conds := []string{}
	args := []any{}
	if mode != "" {
		conds = append(conds, "mode = ?")
		args = append(args, mode)
	}
	if before > 0 {
		conds = append(conds, "created_at > 0 AND created_at < ?")
		args = append(args, before)
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

var (
	errClientErrorFilterRequired = &clientErrorFilterError{"至少需要 mode 或 before 一个条件"}
	errInvalidClientErrorBefore  = &clientErrorFilterError{"before 必须是毫秒时间戳"}
)

type clientErrorFilterError struct{ msg string }

func (e *clientErrorFilterError) Error() string { return e.msg }

// DELETE /api/client-errors/{id}（superOnly）
func (h *handlers) clientErrorDeleteOne(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "无效的错误 ID")
		return
	}
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM client_errors WHERE id = ?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, http.StatusNotFound, "客户端错误不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": n})
}

// DELETE /api/client-errors?mode=&before=（superOnly）
func (h *handlers) clientErrorDeleteBatch(w http.ResponseWriter, r *http.Request) {
	where, args, err := clientErrorAdminFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM client_errors`+where, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "批量删除失败")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": n})
}

// GET /api/client-errors/export（superOnly，最多 10000 条）
func (h *handlers) clientErrorExport(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	qs := r.URL.Query()
	mode := truncateRunes(strings.ToLower(strings.TrimSpace(qs.Get("mode"))), clientErrorMode)
	beforeRaw := strings.TrimSpace(qs.Get("before"))
	var before int64
	if beforeRaw != "" {
		n, err := strconv.ParseInt(beforeRaw, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, errInvalidClientErrorBefore.Error())
			return
		}
		before = n
	}
	conds := []string{}
	args := []any{}
	if mode != "" {
		conds = append(conds, "mode = ?")
		args = append(args, mode)
	}
	if before > 0 {
		conds = append(conds, "created_at > 0 AND created_at < ?")
		args = append(args, before)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, COALESCE(created_at,0), COALESCE(mode,''), COALESCE(page,''),
		        COALESCE(url,''), COALESCE(message,''), COALESCE(ua,'')
		   FROM client_errors`+where+`
		  ORDER BY created_at DESC, id DESC
		  LIMIT 10000`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "导出失败")
		return
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="client-errors.csv"`)
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "created_at", "mode", "page", "url", "message", "ua"})
	for rows.Next() {
		var (
			id        int64
			createdAt int64
			modeVal   string
			pageVal   string
			urlVal    string
			message   string
			ua        string
		)
		if err := rows.Scan(&id, &createdAt, &modeVal, &pageVal, &urlVal, &message, &ua); err != nil {
			return
		}
		_ = cw.Write([]string{
			strconv.FormatInt(id, 10),
			strconv.FormatInt(createdAt, 10),
			modeVal, pageVal, urlVal, message, ua,
		})
	}
	cw.Flush()
}
