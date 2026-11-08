package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/vpn"
)

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func newUserCode() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	s := strings.ToUpper(hex.EncodeToString(buf))
	return s[:4] + "-" + s[4:], nil
}

// POST /api/vpn/device/code — 设备码申请（未登录）。
func (h *handlers) vpnDeviceCodeRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deviceCode, err := randomToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成设备码失败")
		return
	}
	userCode, err := newUserCode()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成设备码失败")
		return
	}
	expires := time.Now().Add(10 * time.Minute)
	if err := h.vpnStore().CreateDeviceCode(r.Context(), deviceCode, userCode, expires); err != nil {
		writeError(w, http.StatusInternalServerError, "写入设备码失败")
		return
	}
	origin := strings.TrimRight(requestOrigin(r), "/")
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code":     deviceCode,
		"user_code":       userCode,
		"verification_uri": origin + "/portal?device=" + userCode,
		"expires_in":      600,
		"interval":        2,
	})
}

// POST /api/vpn/device/token — 轮询换取登录令牌（未登录）。
func (h *handlers) vpnDeviceCodeToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		DeviceCode string `json:"device_code"`
		Name       string `json:"name"`
		Platform   string `json:"platform"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	body.DeviceCode = strings.TrimSpace(body.DeviceCode)
	if body.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "缺少 device_code")
		return
	}
	uuid, approved, expires, err := h.vpnStore().GetDeviceCode(r.Context(), body.DeviceCode)
	if err == vpn.ErrNotFound {
		writeError(w, http.StatusBadRequest, "设备码无效")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询设备码失败")
		return
	}
	if time.Now().After(expires) {
		writeError(w, http.StatusGone, "设备码已过期")
		return
	}
	if !approved || uuid == "" {
		writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "authorization_pending"})
		return
	}

	var email string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT email FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).Scan(&email); err != nil || email == "" {
		writeError(w, http.StatusBadRequest, "该 UUID 未绑定邮箱，无法登录")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "desktop"
	}
	deviceID, err := h.vpnStore().CreateDevice(r.Context(), uuid, name, strings.TrimSpace(body.Platform), "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建设备失败")
		return
	}
	token, err := auth.IssuePortalToken(email, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发登录令牌失败")
		return
	}
	_ = h.vpnStore().DeleteDeviceCode(r.Context(), body.DeviceCode)
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"device_id":    deviceID,
		"uuid":         uuid,
		"email":        email,
	})
}

// POST /api/portal/vpn/device/approve — 用户在门户确认设备码。
func (h *handlers) vpnPortalDeviceApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uuid, ok := h.portalVPNUUID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录或 UUID 不属于当前账号")
		return
	}
	var body struct {
		UserCode string `json:"user_code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(body.UserCode))
	if err := h.vpnStore().ApproveDeviceCode(r.Context(), code, uuid); err != nil {
		writeError(w, http.StatusBadRequest, "设备码无效或已过期")
		return
	}
	h.vpnAudit(r.Context(), "portal:"+uuid, "vpn.device.approve", code, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GET /api/portal/vpn/devices
func (h *handlers) vpnPortalDevices(w http.ResponseWriter, r *http.Request) {
	uuid, ok := h.portalVPNUUID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录或 UUID 不属于当前账号")
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := h.vpnStore().ListDevices(r.Context(), uuid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询设备失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": list})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// DELETE /api/portal/vpn/devices/{id}
func (h *handlers) vpnPortalDeviceItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	uuid, ok := h.portalVPNUUID(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "未登录或 UUID 不属于当前账号")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	dev, err := h.vpnStore().GetDevice(r.Context(), id)
	if err == vpn.ErrNotFound || (err == nil && dev.UUID != uuid) {
		writeError(w, http.StatusNotFound, "设备不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询设备失败")
		return
	}
	if err := h.vpnStore().RevokeDevice(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "吊销失败")
		return
	}
	h.vpnAudit(r.Context(), "portal:"+uuid, "vpn.device.revoke", r.PathValue("id"), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

