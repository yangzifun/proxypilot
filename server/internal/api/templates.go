package api

import (
	"net/http"
	"strings"
)

// GET /api/templates（公开：登录页/生成页的模板下拉使用）
func (h *handlers) templatesList(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	all := qs.Get("all") == "1" || strings.EqualFold(qs.Get("all"), "true")
	page := atoiDefault(qs.Get("page"), 1)
	size := atoiDefault(qs.Get("size"), 10)
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 1000 {
		size = 1000
	}

	order := map[string]string{
		"name": "name", "url": "url", "is_default": "is_default", "created_at": "created_at",
	}[qs.Get("sort")]
	if order == "" {
		order = "created_at"
	}
	dir := "DESC"
	if strings.EqualFold(qs.Get("order"), "asc") {
		dir = "ASC"
	}

	where := ""
	var args []any
	if q := strings.TrimSpace(qs.Get("q")); q != "" {
		where = ` WHERE (name LIKE ? OR url LIKE ? OR IFNULL(description,'') LIKE ?)`
		like := "%" + q + "%"
		args = append(args, like, like, like)
	}

	var total int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM config_templates`+where, args...).Scan(&total); err != nil {
		h.log.Error("统计模板失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}

	sqlText := `SELECT id, name, url, COALESCE(description,''), COALESCE(is_default,0),
	                   COALESCE(created_at,0), COALESCE(updated_at,0)
	              FROM config_templates` + where + ` ORDER BY ` + order + ` ` + dir
	qargs := append([]any{}, args...)
	if !all {
		sqlText += " LIMIT ? OFFSET ?"
		qargs = append(qargs, size, (page-1)*size)
	}

	rows, err := h.db.QueryContext(r.Context(), sqlText, qargs...)
	if err != nil {
		h.log.Error("查询模板失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	defer rows.Close()

	type tpl struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		Description string `json:"description"`
		IsDefault   int64  `json:"is_default"`
		CreatedAt   int64  `json:"created_at"`
		UpdatedAt   int64  `json:"updated_at"`
	}
	data := make([]tpl, 0, size)
	for rows.Next() {
		var it tpl
		if err := rows.Scan(&it.ID, &it.Name, &it.URL, &it.Description, &it.IsDefault, &it.CreatedAt, &it.UpdatedAt); err != nil {
			writeError(w, http.StatusInternalServerError, "解析模板失败")
			return
		}
		data = append(data, it)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "读取模板失败")
		return
	}

	respPage, respSize := page, size
	if all {
		respPage, respSize = 1, len(data)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   total,
		"page":    respPage,
		"size":    respSize,
		"data":    data,
	})
}
