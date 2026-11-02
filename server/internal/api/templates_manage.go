package api

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var httpURLRe = regexp.MustCompile(`^https?://`)

type templateBody struct {
	ID          *int64  `json:"id"`
	Name        *string `json:"name"`
	URL         *string `json:"url"`
	Description *string `json:"description"`
	IsDefault   any     `json:"is_default"`
}

func (h *handlers) templateCreate(w http.ResponseWriter, r *http.Request) {
	h.templateWrite(w, r, nil)
}

func (h *handlers) templateUpdate(w http.ResponseWriter, r *http.Request) {
	h.templateWrite(w, r, nil)
}

func (h *handlers) templateItemPut(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	h.templateWrite(w, r, &id)
}

// templateWrite 复刻 writeTemplate：不传的字段保持原值，is_default=1 会先把其它模板置 0。
func (h *handlers) templateWrite(w http.ResponseWriter, r *http.Request, pathID *int64) {
	var body templateBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}

	id := pathID
	if id == nil && body.ID != nil {
		id = body.ID
	}
	var (
		curName, curURL, curDesc sql.NullString
		curDefault               int64
		exists                   bool
	)
	if id != nil {
		err := h.db.QueryRowContext(r.Context(),
			`SELECT name, url, COALESCE(description,''), COALESCE(is_default,0)
			   FROM config_templates WHERE id = ? LIMIT 1`, *id).
			Scan(&curName, &curURL, &curDesc, &curDefault)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "模板不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询模板失败")
			return
		}
		exists = true
	}

	pick := func(p *string, cur sql.NullString) string {
		if p != nil {
			return strings.TrimSpace(*p)
		}
		return cur.String
	}
	name := pick(body.Name, curName)
	url := pick(body.URL, curURL)
	description := pick(body.Description, curDesc)

	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写模板名称")
		return
	}
	if len([]rune(name)) > 100 {
		writeError(w, http.StatusBadRequest, "模板名称过长（≤100 字符）")
		return
	}
	if !httpURLRe.MatchString(url) {
		writeError(w, http.StatusBadRequest, "URL 需以 http:// 或 https:// 开头")
		return
	}

	var dup int64
	notID := int64(0)
	if id != nil {
		notID = *id
	}
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM config_templates WHERE name = ? AND id <> ? LIMIT 1`, name, notID).Scan(&dup); err == nil {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}

	isDefault := curDefault
	if body.IsDefault != nil {
		if parseTruthy(body.IsDefault) {
			isDefault = 1
		} else {
			isDefault = 0
		}
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存模板失败")
		return
	}
	defer tx.Rollback()
	if isDefault == 1 {
		if _, err := tx.ExecContext(r.Context(), `UPDATE config_templates SET is_default = 0`); err != nil {
			writeError(w, http.StatusInternalServerError, "保存模板失败")
			return
		}
	}
	now := nowMillis()
	if exists {
		if _, err := tx.ExecContext(r.Context(),
			`UPDATE config_templates SET name = ?, url = ?, description = ?, is_default = ?, updated_at = ?
			  WHERE id = ?`, name, url, description, isDefault, now, *id); err != nil {
			writeError(w, http.StatusInternalServerError, "保存模板失败")
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, "保存模板失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": *id, "created": false})
		return
	}

	res, err := tx.ExecContext(r.Context(),
		`INSERT INTO config_templates (name, url, description, is_default, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`, name, url, description, isDefault, now, now)
	if err != nil {
		h.log.Error("创建模板失败", "err", err)
		writeError(w, http.StatusInternalServerError, "保存模板失败")
		return
	}
	newID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "保存模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": newID, "created": true})
}

// GET /api/templates/manage/{id}
func (h *handlers) templateItemGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var (
		it struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			URL         string `json:"url"`
			Description string `json:"description"`
			IsDefault   int64  `json:"is_default"`
			CreatedAt   int64  `json:"created_at"`
			UpdatedAt   int64  `json:"updated_at"`
		}
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id, name, url, COALESCE(description,''), COALESCE(is_default,0),
		        COALESCE(created_at,0), COALESCE(updated_at,0)
		   FROM config_templates WHERE id = ? LIMIT 1`, id).
		Scan(&it.ID, &it.Name, &it.URL, &it.Description, &it.IsDefault, &it.CreatedAt, &it.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": it})
}

// DELETE /api/templates/manage[/{id}]  body: {"id": 1}
func (h *handlers) templateDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		var body struct {
			ID *int64 `json:"id"`
		}
		_ = decodeJSON(w, r, &body)
		if body.ID == nil {
			writeError(w, http.StatusBadRequest, "ID Required")
			return
		}
		id = *body.ID
	}

	var isDefault int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(is_default,0) FROM config_templates WHERE id = ? LIMIT 1`, id).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	if isDefault == 1 {
		writeError(w, http.StatusBadRequest, "不能删除默认模板")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM config_templates WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": 1})
}

// POST /api/templates/manage/set-default  body: {"id": 1}
func (h *handlers) templateSetDefault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID *int64 `json:"id"`
	}
	if err := decodeJSON(w, r, &body); err != nil || body.ID == nil || *body.ID <= 0 {
		writeError(w, http.StatusBadRequest, "ID Required")
		return
	}
	var exists int64
	err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM config_templates WHERE id = ? LIMIT 1`, *body.ID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "设置默认模板失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `UPDATE config_templates SET is_default = 0`); err != nil {
		writeError(w, http.StatusInternalServerError, "设置默认模板失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE config_templates SET is_default = 1, updated_at = ? WHERE id = ?`, nowMillis(), *body.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "设置默认模板失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "设置默认模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": *body.ID})
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func parseTruthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		return s == "1" || s == "true"
	default:
		return false
	}
}
