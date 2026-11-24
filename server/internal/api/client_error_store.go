package api

import (
	"context"
	"net/http"
	"strings"
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

	where := ""
	args := []any{}
	if mode != "" {
		where = " WHERE mode = ?"
		args = append(args, mode)
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
