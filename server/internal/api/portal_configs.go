package api

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// adminOrPortal 解析 Bearer：后台令牌返回 admin=true；用户端令牌返回 email。
func (h *handlers) adminOrPortal(r *http.Request) (email string, admin bool, ok bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(raw, "Bearer ") {
		return "", false, false
	}
	claims, err := auth.Parse(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")), h.cfg.JWTSecret)
	if err != nil {
		return "", false, false
	}
	// mfa / email_login 事务票据不是会话，不能当管理员或门户用户使用。
	if !claims.IsAccessToken() {
		return "", false, false
	}
	if claims.Role == "user" {
		return normalizeEmail(claims.Sub), false, true
	}
	return "", true, true
}

func (h *handlers) ownsUUID(r *http.Request, email, uuid string) bool {
	var n int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_info WHERE uuid = ? AND LOWER(email) = ?`, uuid, email).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// requireManage 校验「管理员或该 UUID 的拥有者」；失败时已写出响应。
func (h *handlers) requireManage(w http.ResponseWriter, r *http.Request, uuid string) bool {
	email, admin, ok := h.adminOrPortal(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先登录")
		return false
	}
	if admin {
		return true
	}
	if !h.ownsUUID(r, email, uuid) {
		writeError(w, http.StatusForbidden, "该 UUID 不属于当前账号")
		return false
	}
	return true
}

// GET /manage/configs/{uuid}
func (h *handlers) manageConfigsList(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !h.requireManage(w, r, uuid) {
		return
	}
	qs := r.URL.Query()
	protocol := qs.Get("protocol")
	q := qs.Get("q")
	page := atoiDefault(qs.Get("page"), 1)
	size := atoiDefault(qs.Get("pageSize"), 10)
	if size < 1 {
		size = 10
	}

	data, total, err := h.queryConfigs(r.Context(), uuid, protocol, q, page, size)
	if err != nil {
		h.log.Error("查询配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询配置失败")
		return
	}
	pageSize := size
	totalPages := 0
	if pageSize > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uuid":    uuid,
		"configs": data,
		"pagination": map[string]any{
			"page": page, "pageSize": pageSize, "total": total, "totalPages": totalPages,
		},
		"filters": map[string]any{"protocol": protocol, "q": q},
	})
}

// POST /manage/configs  body: {"uuid": "...", "config_data": "..."}
func (h *handlers) manageConfigsAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID       string `json:"uuid"`
		ConfigData string `json:"config_data"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	n, err := h.insertConfigLines(r.Context(), uuid, req.ConfigData)
	if err != nil {
		h.log.Error("写入配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "写入配置失败")
		return
	}
	if n == 0 {
		writeError(w, http.StatusBadRequest, "无有效配置")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "inserted": n})
}

// PUT /manage/configs  body: {"id": 1, "uuid": "...", "config_data": "..."}
func (h *handlers) manageConfigsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int64  `json:"id"`
		UUID       string `json:"uuid"`
		ConfigData string `json:"config_data"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "缺少配置ID")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}

	var exists int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM configs WHERE id = ? AND uuid = ?`, req.ID, uuid).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "配置不存在，或不属于当前UUID，禁止编辑")
		return
	}

	text := strings.TrimSpace(req.ConfigData)
	protocol := getProtocol(text)
	if protocol == "unknown" {
		writeError(w, http.StatusBadRequest, "不支持的配置格式")
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE configs SET config_data = ?, protocol = ?, remark = ?, updated_at = ?
		  WHERE id = ? AND uuid = ?`,
		text, protocol, nullIfEmpty(extractRemark(text, protocol)), nowMillis(), req.ID, uuid)
	if err != nil {
		h.log.Error("更新配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "更新配置失败")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "rows_affected": n,
		"message": map[bool]string{true: "Updated", false: "No changes made"}[n > 0],
	})
}

// DELETE /manage/configs/batch  body: {"ids": [1,2]}
func (h *handlers) manageConfigsBatchDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(w, r, &req); err != nil || len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "无效的ID列表")
		return
	}
	ids := req.IDs
	if len(ids) > 1000 {
		ids = ids[:1000]
	}

	// 逐条校验归属，避免跨账号删除
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT DISTINCT uuid FROM configs WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	var uuids []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			uuids = append(uuids, u)
		}
	}
	rows.Close()
	for _, u := range uuids {
		if !h.requireManage(w, r, u) {
			return
		}
	}

	n, err := deleteConfigsByIDs(r.Context(), h.db, ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": n})
}

// DELETE /manage/configs/id/{id}
func (h *handlers) manageConfigDeleteByID(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "无效的配置ID")
		return
	}
	var uuid string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT uuid FROM configs WHERE id = ? LIMIT 1`, id).Scan(&uuid); err != nil {
		writeError(w, http.StatusNotFound, "配置不存在")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE id = ?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": n})
}

// DELETE /manage/configs/{uuid}
func (h *handlers) manageConfigsClear(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE uuid = ?`, uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "deleted": n})
}

// ---------------- 外部订阅源 ----------------

func validSubSourceURL(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if len(s) > 2048 {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return "", false
	}
	if ip := net.ParseIP(host); ip != nil && isBlockedIP(ip) {
		return "", false
	}
	return u.String(), true
}

// GET /manage/uuid-sub-sources?uuid=
func (h *handlers) manageSubSourcesList(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID Required")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, uuid, url, COALESCE(remark,''), COALESCE(enabled,0), COALESCE(created_at,0), COALESCE(updated_at,0)
		   FROM uuid_sub_sources WHERE uuid = ? ORDER BY id ASC`, uuid)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询外部订阅源失败")
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var (
			id, enabled, createdAt, updatedAt int64
			u, subURL, remark                 string
		)
		if rows.Scan(&id, &u, &subURL, &remark, &enabled, &createdAt, &updatedAt) == nil {
			data = append(data, map[string]any{
				"id": id, "uuid": u, "url": subURL, "remark": remark,
				"enabled": enabled, "created_at": createdAt, "updated_at": updatedAt,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data, "total": len(data)})
}

// POST /manage/uuid-sub-sources  body: {"uuid","url","remark","enabled"}
func (h *handlers) manageSubSourcesAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID    string `json:"uuid"`
		URL     string `json:"url"`
		Remark  string `json:"remark"`
		Enabled *bool  `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	subURL, ok := validSubSourceURL(req.URL)
	if !ok {
		writeError(w, http.StatusBadRequest, "外部订阅URL无效（仅支持 http/https 公网地址）")
		return
	}
	var count int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_sub_sources WHERE uuid = ?`, uuid).Scan(&count)
	if count >= 10 {
		writeError(w, http.StatusBadRequest, "每个 UUID 最多绑定 10 个外部订阅源")
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_sub_sources WHERE uuid = ? AND url = ?`, uuid, subURL).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusBadRequest, "该外部订阅已存在")
		return
	}
	enabled := int64(1)
	if req.Enabled != nil && !*req.Enabled {
		enabled = 0
	}
	remark := strings.TrimSpace(req.Remark)
	if len([]rune(remark)) > 100 {
		remark = string([]rune(remark)[:100])
	}
	now := nowMillis()
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO uuid_sub_sources (uuid, url, remark, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`, uuid, subURL, remark, enabled, now, now)
	if err != nil {
		h.log.Error("添加外部订阅源失败", "err", err)
		writeError(w, http.StatusInternalServerError, "添加外部订阅源失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

// PUT /manage/uuid-sub-sources  body: {"id","url","remark","enabled"}
func (h *handlers) manageSubSourcesUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID      int64   `json:"id"`
		URL     *string `json:"url"`
		Remark  *string `json:"remark"`
		Enabled *bool   `json:"enabled"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "ID Required")
		return
	}

	var (
		curUUID, curURL string
		curRemark       string
		curEnabled      int64
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT uuid, url, COALESCE(remark,''), COALESCE(enabled,0)
		   FROM uuid_sub_sources WHERE id = ? LIMIT 1`, req.ID).
		Scan(&curUUID, &curURL, &curRemark, &curEnabled)
	if err != nil {
		writeError(w, http.StatusNotFound, "外部订阅源不存在")
		return
	}
	if !h.requireManage(w, r, curUUID) {
		return
	}

	subURL := curURL
	if req.URL != nil {
		normalized, ok := validSubSourceURL(*req.URL)
		if !ok {
			writeError(w, http.StatusBadRequest, "外部订阅URL无效（仅支持 http/https 公网地址）")
			return
		}
		subURL = normalized
	}
	remark := curRemark
	if req.Remark != nil {
		remark = strings.TrimSpace(*req.Remark)
		if len([]rune(remark)) > 100 {
			remark = string([]rune(remark)[:100])
		}
	}
	enabled := curEnabled
	if req.Enabled != nil {
		enabled = 0
		if *req.Enabled {
			enabled = 1
		}
	}

	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM uuid_sub_sources WHERE uuid = ? AND url = ? AND id <> ?`,
		curUUID, subURL, req.ID).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusBadRequest, "该外部订阅已存在")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE uuid_sub_sources SET url = ?, remark = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		subURL, remark, enabled, nowMillis(), req.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "更新外部订阅源失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "外部订阅源已更新"})
}

// DELETE /manage/uuid-sub-sources  body: {"id"} 或 {"uuid"}
func (h *handlers) manageSubSourcesDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   int64  `json:"id"`
		UUID string `json:"uuid"`
	}
	_ = decodeJSON(w, r, &req)

	if req.ID > 0 {
		var uuid string
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT uuid FROM uuid_sub_sources WHERE id = ? LIMIT 1`, req.ID).Scan(&uuid); err != nil {
			writeError(w, http.StatusNotFound, "外部订阅源不存在")
			return
		}
		if !h.requireManage(w, r, uuid) {
			return
		}
		if _, err := h.db.ExecContext(r.Context(),
			`DELETE FROM uuid_sub_sources WHERE id = ?`, req.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "删除外部订阅源失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "外部订阅源已删除"})
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "ID Required")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`DELETE FROM uuid_sub_sources WHERE uuid = ?`, uuid); err != nil {
		writeError(w, http.StatusInternalServerError, "删除外部订阅源失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "外部订阅源已删除"})
}
