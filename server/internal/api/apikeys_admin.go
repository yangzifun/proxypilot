package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	apiKeyPrefix     = "pk_"
	apiKeyBytes      = 32
	maxAPIKeys       = 50
	apiKeyPermTokens = "groups:read,groups:write,configs:read,configs:write,templates:read,templates:write"
)

var apiKeyAllowedPerms = map[string]bool{
	"groups:read": true, "groups:write": true,
	"configs:read": true, "configs:write": true,
	"templates:read": true, "templates:write": true,
}

func generateAPIKey() (string, error) {
	buf := make([]byte, apiKeyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return apiKeyPrefix + hex.EncodeToString(buf), nil
}

func apiKeyHint(key string) string {
	s := strings.TrimSpace(key)
	if len(s) <= len(apiKeyPrefix)+8 {
		return s[:minInt(len(s), len(apiKeyPrefix)+2)] + "****"
	}
	return s[:len(apiKeyPrefix)+4] + "****" + s[len(s)-4:]
}

func normalizeAPIKeyPermissions(raw any) string {
	var items []string
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		items = strings.Split(v, ",")
	case []any:
		for _, it := range v {
			items = append(items, stringify(it))
		}
	case []string:
		items = v
	default:
		items = strings.Split(stringify(raw), ",")
	}
	var valid []string
	for _, it := range items {
		p := strings.ToLower(strings.TrimSpace(it))
		if p == "*" {
			return "*"
		}
		if apiKeyAllowedPerms[p] {
			dup := false
			for _, x := range valid {
				if x == p {
					dup = true
					break
				}
			}
			if !dup {
				valid = append(valid, p)
			}
		}
	}
	return strings.Join(valid, ",")
}

func describeAPIKeyPermissions(permissions string) string {
	list := strings.Split(strings.TrimSpace(permissions), ",")
	var labels []string
	labelMap := map[string]string{
		"groups:read": "分组只读", "groups:write": "分组管理",
		"configs:read": "配置只读", "configs:write": "配置管理",
		"templates:read": "模板只读", "templates:write": "模板管理",
	}
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "*" {
			return "全部权限"
		}
		if l, ok := labelMap[p]; ok {
			labels = append(labels, l)
		} else {
			labels = append(labels, p)
		}
	}
	if len(labels) == 0 {
		return "无权限"
	}
	return strings.Join(labels, "、")
}

func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	default:
		return ""
	}
}

func resolveAPIKeyExpiry(inDays, at any) (int64, string) {
	if inDays != nil && strings.TrimSpace(stringify(inDays)) != "" {
		days, err := strconv.ParseFloat(strings.TrimSpace(stringify(inDays)), 64)
		if err != nil || days < 0 {
			return 0, "有效期天数无效"
		}
		if days <= 0 {
			return 0, ""
		}
		if days > 3650 {
			days = 3650
		}
		return time.Now().UnixMilli() + int64(days*24*3600*1000), ""
	}
	if at != nil && strings.TrimSpace(stringify(at)) != "" {
		ts, err := strconv.ParseFloat(strings.TrimSpace(stringify(at)), 64)
		if err != nil || ts < 0 {
			return 0, "有效期时间戳无效"
		}
		return int64(ts), ""
	}
	return 0, ""
}

// GET/POST /api/apikeys
func (h *handlers) apiKeysDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.apiKeysList(w, r)
	case http.MethodPost:
		h.apiKeysCreate(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (h *handlers) apiKeysList(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, key_name, COALESCE(key_hint,''), COALESCE(enabled,0), COALESCE(permissions,''),
		        COALESCE(expires_at,0), COALESCE(created_by,''), COALESCE(created_at,0),
		        COALESCE(updated_at,0), COALESCE(last_used_at,0), COALESCE(usage_count,0)
		   FROM api_keys ORDER BY id ASC`)
	if err != nil {
		h.log.Error("查询 API 密钥失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var (
			id, enabled, expiresAt, createdAt, updatedAt, lastUsed, usage int64
			name, hint, perms, createdBy                                  string
		)
		if rows.Scan(&id, &name, &hint, &enabled, &perms, &expiresAt, &createdBy,
			&createdAt, &updatedAt, &lastUsed, &usage) == nil {
			data = append(data, map[string]any{
				"id": id, "key_name": name, "key_hint": hint, "enabled": enabled,
				"permissions": perms, "permissions_text": describeAPIKeyPermissions(perms),
				"expires_at": expiresAt, "created_by": createdBy,
				"created_at": createdAt, "updated_at": updatedAt,
				"last_used_at": lastUsed, "usage_count": usage,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}

func (h *handlers) apiKeysCreate(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsFrom(r)
	var body struct {
		KeyName      string `json:"key_name"`
		Name         string `json:"name"`
		Permissions  any    `json:"permissions"`
		ExpiresInDay any    `json:"expires_in_days"`
		ExpiresAt    any    `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	name := strings.TrimSpace(body.KeyName)
	if name == "" {
		name = strings.TrimSpace(body.Name)
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写密钥名称")
		return
	}
	if len([]rune(name)) > 60 {
		writeError(w, http.StatusBadRequest, "密钥名称过长（≤60 字符）")
		return
	}
	var n int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM api_keys WHERE key_name = ?`, name).Scan(&n)
	if n > 0 {
		writeError(w, http.StatusConflict, "密钥名称已存在")
		return
	}
	var total int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM api_keys`).Scan(&total)
	if total >= maxAPIKeys {
		writeError(w, http.StatusBadRequest, "密钥数量已达上限（50）")
		return
	}
	perms := normalizeAPIKeyPermissions(body.Permissions)
	if perms == "" {
		writeError(w, http.StatusBadRequest, "请至少选择一项权限")
		return
	}
	expiresAt, msg := resolveAPIKeyExpiry(body.ExpiresInDay, body.ExpiresAt)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	key, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	now := nowMillis()
	createdBy := claims.Sub
	if createdBy == "" {
		createdBy = "admin"
	}
	res, err := h.db.ExecContext(r.Context(),
		`INSERT INTO api_keys (key_name, api_key, secret_key, enabled, permissions, key_hint,
		   expires_at, created_by, created_at, updated_at, last_used_at, usage_count)
		 VALUES (?, ?, NULL, 1, ?, ?, ?, ?, ?, ?, 0, 0)`,
		name, sha256Hex(key), perms, apiKeyHint(key), expiresAt, createdBy, now, now)
	if err != nil {
		h.log.Error("创建 API 密钥失败", "err", err)
		writeError(w, http.StatusInternalServerError, "创建失败")
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "id": id, "key_name": name, "api_key": key,
		"key_hint": apiKeyHint(key), "permissions": perms, "expires_at": expiresAt,
		"message": "密钥仅显示一次，请立即复制保存",
	})
}

// PUT/DELETE /api/apikeys/{id}
func (h *handlers) apiKeyItemDispatch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "API 密钥不存在")
		return
	}
	switch r.Method {
	case http.MethodPut:
		h.apiKeyUpdate(w, r, id)
	case http.MethodDelete:
		h.apiKeyDelete(w, r, id)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (h *handlers) apiKeyUpdate(w http.ResponseWriter, r *http.Request, id int64) {
	var body struct {
		KeyName      *string `json:"key_name"`
		Enabled      any     `json:"enabled"`
		Permissions  any     `json:"permissions"`
		ExpiresInDay any     `json:"expires_in_days"`
		ExpiresAt    any     `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}

	var (
		curName, curPerms string
		curEnabled        int64
		curExpires        int64
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT key_name, COALESCE(enabled,0), COALESCE(permissions,''), COALESCE(expires_at,0)
		   FROM api_keys WHERE id = ? LIMIT 1`, id).
		Scan(&curName, &curEnabled, &curPerms, &curExpires)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "API 密钥不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}

	name := curName
	if body.KeyName != nil {
		name = strings.TrimSpace(*body.KeyName)
	}
	if name == "" {
		writeError(w, http.StatusBadRequest, "请填写密钥名称")
		return
	}
	if len([]rune(name)) > 60 {
		writeError(w, http.StatusBadRequest, "密钥名称过长（≤60 字符）")
		return
	}
	var dup int64
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM api_keys WHERE key_name = ? AND id <> ?`, name, id).Scan(&dup)
	if dup > 0 {
		writeError(w, http.StatusConflict, "密钥名称已存在")
		return
	}
	enabled := curEnabled
	if body.Enabled != nil {
		enabled = 0
		if parseTruthy(body.Enabled) {
			enabled = 1
		}
	}
	perms := curPerms
	if body.Permissions != nil {
		perms = normalizeAPIKeyPermissions(body.Permissions)
	}
	expiresAt := curExpires
	if body.ExpiresInDay != nil || body.ExpiresAt != nil {
		v, msg := resolveAPIKeyExpiry(body.ExpiresInDay, body.ExpiresAt)
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		expiresAt = v
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE api_keys SET key_name = ?, enabled = ?, permissions = ?, expires_at = ?, updated_at = ?
		  WHERE id = ?`, name, enabled, perms, expiresAt, nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id})
}

func (h *handlers) apiKeyDelete(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM api_keys WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": id, "deleted": 1})
}

// POST /api/apikeys/{id}/rotate
func (h *handlers) apiKeyRotate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "API 密钥不存在")
		return
	}
	var exists int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM api_keys WHERE id = ?`, id).Scan(&exists)
	if exists == 0 {
		writeError(w, http.StatusNotFound, "API 密钥不存在")
		return
	}
	key, err := generateAPIKey()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成密钥失败")
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE api_keys SET api_key = ?, key_hint = ?, updated_at = ?, last_used_at = 0, usage_count = 0
		  WHERE id = ?`, sha256Hex(key), apiKeyHint(key), nowMillis(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "轮换失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "id": id, "api_key": key, "key_hint": apiKeyHint(key), "rotated": true,
		"message": "密钥已轮换，旧密钥立即失效；请立即保存新密钥（仅显示一次）",
	})
}
