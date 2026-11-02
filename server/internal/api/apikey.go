package api

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strings"
)

type apiKeyRule struct {
	methods []string
	re      *regexp.Regexp
	perm    string
}

// apiKeyRules 与 worker.js 的 API_KEY_ROUTE_PERMISSIONS 对应（顺序敏感）。
var apiKeyRules = []apiKeyRule{
	{[]string{"GET"}, regexp.MustCompile(`^/api/uuids$`), "groups:read"},
	{[]string{"POST", "PUT", "DELETE"}, regexp.MustCompile(`^/api/uuids$`), "groups:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/uuids/[^/]+$`), "groups:read"},
	{[]string{"PUT", "DELETE"}, regexp.MustCompile(`^/api/uuids/[^/]+$`), "groups:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/uuids/[^/]+/configs$`), "configs:read"},
	{[]string{"POST", "PUT", "DELETE"}, regexp.MustCompile(`^/api/uuids/[^/]+/configs$`), "configs:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/uuids/[^/]+/configs/\d+$`), "configs:read"},
	{[]string{"PUT", "DELETE"}, regexp.MustCompile(`^/api/uuids/[^/]+/configs/\d+$`), "configs:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/uuid-configs(/stats)?$`), "configs:read"},
	{[]string{"POST", "PUT", "DELETE"}, regexp.MustCompile(`^/api/uuid-configs$`), "configs:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/templates/manage$`), "templates:read"},
	{[]string{"POST", "PUT", "DELETE"}, regexp.MustCompile(`^/api/templates/manage$`), "templates:write"},
	{[]string{"POST"}, regexp.MustCompile(`^/api/templates/manage/set-default$`), "templates:write"},
	{[]string{"GET"}, regexp.MustCompile(`^/api/templates/manage/\d+$`), "templates:read"},
	{[]string{"PUT", "DELETE"}, regexp.MustCompile(`^/api/templates/manage/\d+$`), "templates:write"},
}

func apiKeyPermission(path, method string) string {
	for _, rule := range apiKeyRules {
		for _, m := range rule.methods {
			if m == method && rule.re.MatchString(path) {
				return rule.perm
			}
		}
	}
	return ""
}

func apiKeyHasPermission(permissions, required string) bool {
	if required == "" {
		return false
	}
	list := strings.Split(strings.ToLower(permissions), ",")
	for _, item := range list {
		p := strings.TrimSpace(item)
		if p == "" {
			continue
		}
		if p == "*" || p == required || p == strings.SplitN(required, ":", 2)[0] {
			return true
		}
	}
	return false
}

type apiKeyInfo struct {
	ID          int64
	Name        string
	Permissions string
}

// authenticateAPIKey 校验 X-API-Key：SHA-256 命中优先，兼容历史明文行。
func (h *handlers) authenticateAPIKey(ctx context.Context, raw string) (*apiKeyInfo, int, string) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return nil, http.StatusUnauthorized, "API 密钥无效"
	}
	const q = `SELECT id, COALESCE(key_name,''), COALESCE(permissions,''), COALESCE(enabled,0), COALESCE(expires_at,0)
	             FROM api_keys WHERE api_key = ? LIMIT 1`
	var (
		info      apiKeyInfo
		enabled   int64
		expiresAt int64
	)
	err := h.db.QueryRowContext(ctx, q, sha256Hex(key)).
		Scan(&info.ID, &info.Name, &info.Permissions, &enabled, &expiresAt)
	if err == sql.ErrNoRows {
		// 历史明文行（早期由 SQL 脚本创建）
		err = h.db.QueryRowContext(ctx, q, key).
			Scan(&info.ID, &info.Name, &info.Permissions, &enabled, &expiresAt)
	}
	if err == sql.ErrNoRows {
		return nil, http.StatusUnauthorized, "API 密钥无效"
	}
	if err != nil {
		h.log.Error("查询 API 密钥失败", "err", err)
		return nil, http.StatusInternalServerError, "API 密钥校验失败"
	}
	if enabled != 1 {
		return nil, http.StatusForbidden, "API 密钥已停用"
	}
	if expiresAt > 0 && expiresAt < nowMillis() {
		return nil, http.StatusForbidden, "API 密钥已过期"
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE api_keys SET last_used_at = ?, usage_count = COALESCE(usage_count,0) + 1 WHERE id = ?`,
		nowMillis(), info.ID); err != nil {
		h.log.Warn("更新 API 密钥统计失败", "err", err)
	}
	return &info, 0, ""
}

// serveAPIKey 处理带 X-API-Key 的请求：路由白名单 + 权限位。
func (h *handlers) serveAPIKey(w http.ResponseWriter, r *http.Request, raw string, opts authOpts, next http.HandlerFunc) {
	// API 密钥不具备后台角色，超管专属接口一律拒绝
	if opts.superOnly {
		writeError(w, http.StatusForbidden, "API 密钥不可访问该接口")
		return
	}
	required := apiKeyPermission(r.URL.Path, r.Method)
	if required == "" {
		writeError(w, http.StatusForbidden, "API 密钥不可访问该接口")
		return
	}
	info, status, msg := h.authenticateAPIKey(r.Context(), raw)
	if info == nil {
		writeError(w, status, msg)
		return
	}
	if !apiKeyHasPermission(info.Permissions, required) {
		writeError(w, http.StatusForbidden, "API 密钥权限不足（需要 "+required+"）")
		return
	}
	ctx := context.WithValue(r.Context(), apiKeyPermsKey{}, info.Permissions)
	next(w, r.WithContext(ctx))
}

// apiKeyPermsKey 把 API 密钥权限位带进上下文，供业务处理器做细粒度判定。
type apiKeyPermsKey struct{}

// hasAPIGroupWrite：JWT 请求不受限；API 密钥需要 groups:write（与 Worker 的 apiKeyActorCanCreateGroup 一致）。
func (h *handlers) hasAPIGroupWrite(r *http.Request) bool {
	perms, ok := r.Context().Value(apiKeyPermsKey{}).(string)
	if !ok {
		return true
	}
	return apiKeyHasPermission(perms, "groups:write")
}
