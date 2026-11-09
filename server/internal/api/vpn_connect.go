package api

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// POST /api/portal/vpn/connect — 校验授权并签发连接配置。
func (h *handlers) vpnPortalConnect(w http.ResponseWriter, r *http.Request) {
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
		DeviceID int64 `json:"device_id"`
		NodeID   int64 `json:"node_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	if body.DeviceID <= 0 || body.NodeID <= 0 {
		writeError(w, http.StatusBadRequest, "device_id 与 node_id 必填")
		return
	}

	settings, err := h.vpnStore().LoadSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取 VPN 设置失败")
		return
	}
	if !settings.Enabled {
		writeError(w, http.StatusForbidden, "VPN 功能未开启")
		return
	}

	dev, err := h.vpnStore().GetDevice(r.Context(), body.DeviceID)
	if err == vpn.ErrNotFound {
		writeError(w, http.StatusNotFound, "设备不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询设备失败")
		return
	}
	node, err := h.vpnStore().GetNode(r.Context(), body.NodeID)
	if err == vpn.ErrNotFound {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	access, err := h.vpnStore().GetAccess(r.Context(), uuid)
	if err != nil {
		writeDenied(w, vpn.Deny(vpn.CodeUserNotAuthorized, "用户未开通 VPN"))
		return
	}
	grant, err := h.vpnStore().GetGrant(r.Context(), node.ID, uuid)
	if err != nil {
		writeDenied(w, vpn.Deny(vpn.CodeNodeNotBound, "该节点未授权给此用户"))
		return
	}

	// 同设备重连时先收掉它自己在旧连接里残留的会话（客户端崩溃不会调用 disconnect）。
	_ = h.vpnStore().CloseDeviceSessions(r.Context(), dev.ID, node.ID, "reconnect")
	// 超过 2 倍的令牌 TTL 仍未结束的会话一律视为僵尸会话，释放并发名额。
	if settings, serr := h.vpnStore().LoadSettings(r.Context()); serr == nil {
		ttl := time.Duration(settings.TokenTTLSeconds) * 2 * time.Second
		if ttl <= 0 {
			ttl = 20 * time.Minute
		}
		if n, rerr := h.vpnStore().ReapStaleSessions(r.Context(), ttl); rerr == nil && n > 0 {
			h.log.Warn("回收僵尸 VPN 会话", "count", n)
		}
	}
	userDevices, _ := h.vpnStore().CountUserDevices(r.Context(), uuid)
	nodeSessions, _ := h.vpnStore().ActiveSessionCount(r.Context(), node.ID, uuid)

	authErr := vpn.Authorize(vpn.AuthInput{
		Now: time.Now(),
		UserUUID: uuid,
		Access: vpn.Access{
			Enabled: access.Enabled, ExpiresAt: access.ExpiresAt,
			MaxDevices: access.MaxDevices, QuotaBytes: access.QuotaBytes, UsedBytes: access.UsedBytes,
		},
		Node:  vpn.Node{ID: node.ID, State: node.State, Endpoint: node.Endpoint},
		Grant: &vpn.Grant{ID: grant.ID, NodeID: grant.NodeID, SubjectID: grant.SubjectID, Enabled: grant.Enabled, ExpiresAt: grant.ExpiresAt, MaxDevices: grant.MaxDevices, QuotaBytes: grant.QuotaBytes, UsedBytes: grant.UsedBytes},
		Device: vpn.Device{ID: dev.ID, UUID: dev.UUID, State: dev.State},
		UserDeviceCount: userDevices,
		NodeDeviceCount: nodeSessions,
	})
	if authErr != nil {
		writeDenied(w, authErr)
		return
	}
	if strings.TrimSpace(node.ClientCIDR) == "" {
		writeError(w, http.StatusConflict, "节点尚未分配客户端子网")
		return
	}

	ip4, err := h.vpnStore().AllocateIP(r.Context(), node.ID, dev.ID, node.ClientCIDR)
	if err != nil {
		writeError(w, http.StatusConflict, "地址分配失败: "+err.Error())
		return
	}
	signer, err := h.vpnSigner(r.Context())
	if err != nil {
		h.log.Error("VPN 签名密钥不可用", "err", err)
		writeError(w, http.StatusInternalServerError, "签发连接令牌失败")
		return
	}
	claims := vpn.Claims{
		Subject: fmt.Sprintf("device:%d", dev.ID),
		UUID:    uuid,
		NodeID:  node.ID,
		GrantID: grant.ID,
		IPv4:    ip4,
		Network: "default",
	}
	token, err := signer.Issue(claims, settings.TokenTTL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签发连接令牌失败")
		return
	}
	sessionID, err := h.vpnStore().CreateSession(r.Context(), dev.ID, node.ID, grant.ID, ip4, clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	_ = h.vpnStore().TouchDevice(r.Context(), dev.ID, ip4)
	h.vpnAudit(r.Context(), "portal:"+uuid, "vpn.connect", fmt.Sprintf("node=%d", node.ID), ip4)

	host, _, _ := net.SplitHostPort(node.Endpoint)
	sni := node.Domain
	if sni == "" {
		sni = host
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session_id": sessionID,
		"node": map[string]any{
			"id": node.ID, "name": node.Name, "region": node.Region, "endpoint": node.Endpoint,
		},
		"token": token,
		"ip4":   ip4,
		"mtu":   settings.MTU,
		// 组网网段与节点客户端子网由服务端下发，客户端不再自行填写
		"mesh_cidr":   settings.MeshCIDR4,
		"client_cidr": node.ClientCIDR,
		"dns":   []string{cidrGateway(node.ClientCIDR)},
		"routes": []map[string]string{
			{"dst": "0.0.0.0/0", "note": "全流量（节点 NAT）"},
			{"dst": node.ClientCIDR, "note": "本节点客户端子网"},
		},
		"tls": map[string]string{
			"server_name": sni,
			"mode":        node.CertMode,
		},
		"expires_in": settings.TokenTTLSeconds,
	})
}

// POST /api/portal/vpn/disconnect
func (h *handlers) vpnPortalDisconnect(w http.ResponseWriter, r *http.Request) {
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
		SessionID int64 `json:"session_id"`
	}
	if err := decodeJSON(w, r, &body); err != nil || body.SessionID <= 0 {
		writeError(w, http.StatusBadRequest, "session_id 必填")
		return
	}
	var n int
	err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM vpn_sessions s JOIN vpn_devices d ON d.id = s.device_id
		  WHERE s.id = ? AND d.uuid = ?`, body.SessionID, uuid).Scan(&n)
	if err != nil || n == 0 {
		writeError(w, http.StatusNotFound, "会话不存在")
		return
	}
	if err := h.vpnStore().CloseSession(r.Context(), body.SessionID, "client"); err != nil {
		writeError(w, http.StatusInternalServerError, "结束会话失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeDenied(w http.ResponseWriter, err error) {
	var d *vpn.DeniedError
	if errors.As(err, &d) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": d.Message, "code": d.Code})
		return
	}
	writeError(w, http.StatusForbidden, err.Error())
}

// cidrGateway 返回客户端子网的第一个可用地址（x.x.x.1）。
func cidrGateway(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	base := ipnet.IP.To4()
	if base == nil {
		return ""
	}
	return net.IPv4(base[0], base[1], base[2], 1).String()
}
