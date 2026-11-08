package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// portalVPNUUID 解析用户中心当前登录邮箱下的 UUID：
// 优先使用 ?uuid=，并校验它确实属于该邮箱；否则取第一个 UUID。
func (h *handlers) portalVPNUUID(r *http.Request) (string, bool) {
	email, ok := h.portalSession(r)
	if !ok {
		return "", false
	}
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	if uuid != "" {
		var n int
		err := h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM uuid_info WHERE uuid = ? AND LOWER(email) = ?`,
			uuid, email).Scan(&n)
		if err != nil || n == 0 {
			return "", false
		}
		return uuid, true
	}
	var first string
	err := h.db.QueryRowContext(r.Context(),
		`SELECT uuid FROM uuid_info WHERE LOWER(email) = ? ORDER BY created_at ASC LIMIT 1`,
		email).Scan(&first)
	if err != nil {
		return "", false
	}
	return first, true
}

// GET /api/portal/vpn/entitlement — 我的 VPN 资格。
func (h *handlers) vpnPortalEntitlement(w http.ResponseWriter, r *http.Request) {
	uuid, ok := h.portalVPNUUID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录或 UUID 不属于当前账号")
		return
	}
	access, err := h.vpnStore().GetAccess(r.Context(), uuid)
	if err == vpn.ErrNotFound {
		writeJSON(w, http.StatusOK, map[string]any{
			"uuid": uuid, "enabled": false, "exists": false,
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	devices, _ := h.vpnStore().CountUserDevices(r.Context(), uuid)
	settings, _ := h.vpnStore().LoadSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"uuid":         access.UUID,
		"enabled":      access.Enabled,
		"exists":       true,
		"expires_at":   access.ExpiresAt.Unix(),
		"max_devices":  access.MaxDevices,
		"devices":      devices,
		"quota_bytes":  access.QuotaBytes,
		"used_bytes":   access.UsedBytes,
		"vpn_enabled":  settings.Enabled,
		"token_ttl":    settings.TokenTTLSeconds,
		"client_cidr4": settings.ClientCIDR4,
	})
}

// GET /api/portal/vpn/nodes — 仅返回已绑定且资格有效的节点。
func (h *handlers) vpnPortalNodes(w http.ResponseWriter, r *http.Request) {
	uuid, ok := h.portalVPNUUID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录或 UUID 不属于当前账号")
		return
	}
	settings, err := h.vpnStore().LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取设置失败")
		return
	}
	if !settings.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{
			"uuid": uuid, "enabled": false, "nodes": []any{}, "message": "VPN 功能未开启",
		})
		return
	}

	now := time.Now()
	list, err := h.vpnStore().ListVisibleNodes(r.Context(), uuid, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	type nodeView struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		Region     string `json:"region"`
		Endpoint   string `json:"endpoint"`
		Online     bool   `json:"online"`
		ExpiresAt  int64  `json:"expires_at"`
		MaxDevices int    `json:"max_devices"`
		QuotaBytes int64  `json:"quota_bytes"`
		UsedBytes  int64  `json:"used_bytes"`
	}
	out := make([]nodeView, 0, len(list))
	for _, v := range list {
		out = append(out, nodeView{
			ID:         v.Node.ID,
			Name:       v.Node.Name,
			Region:     v.Node.Region,
			Endpoint:   v.Node.Endpoint,
			Online:     !v.Node.LastSeen.IsZero() && now.Sub(v.Node.LastSeen) < 2*time.Minute,
			ExpiresAt:  v.ExpiresAt.Unix(),
			MaxDevices: v.MaxDevices,
			QuotaBytes: v.QuotaBytes,
			UsedBytes:  v.UsedBytes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"uuid": uuid, "enabled": true, "nodes": out,
	})
}



