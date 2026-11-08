package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

func (h *handlers) vpnStore() *vpn.Store { return vpn.NewStore(h.db) }

func (h *handlers) vpnAudit(ctx context.Context, actor, action, target, detail string) {
	_, _ = h.db.ExecContext(ctx,
		`INSERT INTO vpn_audit (actor, action, target, detail, created_at) VALUES (?, ?, ?, ?, ?)`,
		actor, action, target, detail, time.Now().UnixMilli())
}

// ---------------------------------------------------------------------------
// 设置
// ---------------------------------------------------------------------------

func (h *handlers) vpnAdminSettings(w http.ResponseWriter, r *http.Request) {
	st := h.vpnStore()
	switch r.Method {
	case http.MethodGet:
		s, err := st.LoadSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s)
	case http.MethodPut:
		var in vpn.Settings
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "参数错误")
			return
		}
		if err := st.SaveSettings(r.Context(), in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.settings.update", "settings", "")
		s, _ := st.LoadSettings(r.Context())
		writeJSON(w, http.StatusOK, s)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// ---------------------------------------------------------------------------
// 节点
// ---------------------------------------------------------------------------

type vpnNodeReq struct {
	ServerID   int64  `json:"server_id"`
	Name       string `json:"name"`
	Domain     string `json:"domain"`
	Region     string `json:"region"`
	Endpoint   string `json:"endpoint"`
	MeshIPv4   string `json:"mesh_ip4"`
	ClientCIDR string `json:"client_cidr4"`
	CertMode   string `json:"cert_mode"`
	ListenPort int    `json:"listen_port"`
	Capacity   int    `json:"capacity"`
	State      string `json:"state"`
}

func (h *handlers) vpnAdminNodes(w http.ResponseWriter, r *http.Request) {
	st := h.vpnStore()
	switch r.Method {
	case http.MethodGet:
		nodes, err := st.ListNodes(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	case http.MethodPost:
		var in vpnNodeReq
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "参数错误")
			return
		}
		if in.ServerID <= 0 {
			writeError(w, http.StatusBadRequest, "server_id 必填")
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			in.Name = fmt.Sprintf("vpn-%d", in.ServerID)
		}
		domain, derr := normalizeNodeDomain(in.Domain)
		if derr != nil {
			writeError(w, http.StatusBadRequest, derr.Error())
			return
		}
		if in.ListenPort <= 0 {
			in.ListenPort = 443
		}
		if strings.TrimSpace(in.Endpoint) == "" {
			in.Endpoint = fmt.Sprintf("%s:%d", domain, in.ListenPort)
		}
		if in.Capacity <= 0 {
			in.Capacity = 500
		}
		if in.State == "" {
			in.State = "pending"
		}
		if in.CertMode == "" {
			in.CertMode = "internal_ca"
		}
		id, err := st.CreateNode(r.Context(), vpn.NodeRecord{
			ServerID:   in.ServerID,
			Name:       strings.TrimSpace(in.Name),
			Domain:     domain,
			Region:     strings.TrimSpace(in.Region),
			Endpoint:   strings.TrimSpace(in.Endpoint),
			MeshIPv4:   strings.TrimSpace(in.MeshIPv4),
			ClientCIDR: strings.TrimSpace(in.ClientCIDR),
			State:      in.State,
			ListenPort: in.ListenPort,
			Capacity:   in.Capacity,
			CertMode:   in.CertMode,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.node.create", strconv.FormatInt(id, 10), in.Name)
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handlers) vpnAdminNodeItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	st := h.vpnStore()
	switch r.Method {
	case http.MethodGet:
		n, err := st.GetNode(r.Context(), id)
		if errors.Is(err, vpn.ErrNotFound) {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, n)
	case http.MethodPut:
		var in vpnNodeReq
		if err := decodeJSON(w, r, &in); err != nil {
			writeError(w, http.StatusBadRequest, "参数错误")
			return
		}
		cur, err := st.GetNode(r.Context(), id)
		if errors.Is(err, vpn.ErrNotFound) {
			writeError(w, http.StatusNotFound, "节点不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if in.Name != "" {
			cur.Name = strings.TrimSpace(in.Name)
		}
		if in.Domain != "" {
			domain, derr := normalizeNodeDomain(in.Domain)
			if derr != nil {
				writeError(w, http.StatusBadRequest, derr.Error())
				return
			}
			cur.Domain = domain
			if strings.TrimSpace(in.Endpoint) == "" {
				cur.Endpoint = fmt.Sprintf("%s:%d", domain, cur.ListenPort)
			}
		}
		if in.Region != "" {
			cur.Region = strings.TrimSpace(in.Region)
		}
		if in.Endpoint != "" {
			cur.Endpoint = strings.TrimSpace(in.Endpoint)
		}
		if in.MeshIPv4 != "" {
			cur.MeshIPv4 = strings.TrimSpace(in.MeshIPv4)
		}
		if in.ClientCIDR != "" {
			cur.ClientCIDR = strings.TrimSpace(in.ClientCIDR)
		}
		if in.CertMode != "" {
			cur.CertMode = in.CertMode
		}
		if in.ListenPort > 0 {
			cur.ListenPort = in.ListenPort
		}
		if in.Capacity > 0 {
			cur.Capacity = in.Capacity
		}
		if in.State != "" {
			cur.State = in.State
		}
		if err := st.UpdateNode(r.Context(), *cur); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.node.update", strconv.FormatInt(id, 10), "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case http.MethodDelete:
		if err := st.DeleteNode(r.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.node.delete", strconv.FormatInt(id, 10), "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// vpnAdminNodeAction 处理 approve / disable / enable。
func (h *handlers) vpnAdminNodeAction(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "参数错误")
		return
	}
	st := h.vpnStore()
	n, err := st.GetNode(r.Context(), id)
	if errors.Is(err, vpn.ErrNotFound) {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	switch strings.ToLower(body.Action) {
	case "approve", "enable":
		if n.MeshIPv4 == "" || n.ClientCIDR == "" {
			mesh, cidr, aerr := h.vpnAllocateNodeAddrs(r.Context(), id)
			if aerr != nil {
				writeError(w, http.StatusBadRequest, aerr.Error())
				return
			}
			n.MeshIPv4, n.ClientCIDR = mesh, cidr
		}
		n.State = "active"
	case "disable":
		n.State = "disabled"
	default:
		writeError(w, http.StatusBadRequest, "action 必须是 approve/enable/disable")
		return
	}
	if err := st.UpdateNode(r.Context(), *n); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.vpnAudit(r.Context(), actorOf(r), "vpn.node."+body.Action, strconv.FormatInt(id, 10), n.State)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": n.State, "mesh_ip4": n.MeshIPv4, "client_cidr4": n.ClientCIDR})
}

// vpnAllocateNodeAddrs 分配管理网地址与客户端子网（10.99.0.11+ / 10.88.N.0/24）。
func (h *handlers) vpnAllocateNodeAddrs(ctx context.Context, nodeID int64) (string, string, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, mesh_ip4, client_cidr4 FROM vpn_nodes WHERE id <> ?`, nodeID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	usedMesh := map[string]bool{}
	usedCIDR := map[string]bool{}
	for rows.Next() {
		var id int64
		var mesh, cidr string
		if err := rows.Scan(&id, &mesh, &cidr); err != nil {
			return "", "", err
		}
		if mesh != "" {
			usedMesh[mesh] = true
		}
		if cidr != "" {
			usedCIDR[cidr] = true
		}
	}
	for i := 11; i <= 250; i++ {
		ip := fmt.Sprintf("10.99.0.%d", i)
		if usedMesh[ip] {
			continue
		}
		for j := 1; j <= 250; j++ {
			cidr := fmt.Sprintf("10.88.%d.0/24", j)
			if usedCIDR[cidr] {
				continue
			}
			return ip, cidr, nil
		}
	}
	return "", "", fmt.Errorf("管理网地址或客户端子网已耗尽")
}

// ---------------------------------------------------------------------------
// 绑定
// ---------------------------------------------------------------------------

func (h *handlers) vpnAdminGrants(w http.ResponseWriter, r *http.Request) {
	nodeID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || nodeID <= 0 {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	st := h.vpnStore()
	switch r.Method {
	case http.MethodGet:
		grants, err := st.ListGrants(r.Context(), nodeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"grants": grants})
	case http.MethodPost:
		var body struct {
			SubjectID  string `json:"subject_id"`
			ExpiresAt  int64  `json:"expires_at"` // unix 秒，0=长期
			MaxDevices int    `json:"max_devices"`
			QuotaBytes int64  `json:"quota_bytes"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "参数错误")
			return
		}
		body.SubjectID = strings.TrimSpace(body.SubjectID)
		if body.SubjectID == "" {
			writeError(w, http.StatusBadRequest, "subject_id（用户 UUID）必填")
			return
		}
		id, err := st.CreateGrant(r.Context(), nodeID, body.SubjectID,
			unixOrZero(body.ExpiresAt), body.MaxDevices, body.QuotaBytes, actorOf(r))
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.grant.create",
			strconv.FormatInt(nodeID, 10), body.SubjectID)
		writeJSON(w, http.StatusOK, map[string]any{"id": id})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *handlers) vpnAdminGrantItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id 非法")
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := h.vpnStore().RevokeGrant(r.Context(), id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.vpnAudit(r.Context(), actorOf(r), "vpn.grant.revoke", strconv.FormatInt(id, 10), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------------------------------------------------------------------------
// 用户资格
// ---------------------------------------------------------------------------

func (h *handlers) vpnAdminUserAccess(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "uuid 必填")
		return
	}
	st := h.vpnStore()
	switch r.Method {
	case http.MethodGet:
		a, err := st.GetAccess(r.Context(), uuid)
		if errors.Is(err, vpn.ErrNotFound) {
			writeJSON(w, http.StatusOK, map[string]any{"uuid": uuid, "enabled": false, "exists": false})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"uuid": a.UUID, "enabled": a.Enabled, "expires_at": a.ExpiresAt.Unix(),
			"max_devices": a.MaxDevices, "quota_bytes": a.QuotaBytes,
			"used_bytes": a.UsedBytes, "note": a.Note, "exists": true,
		})
	case http.MethodPut:
		var body struct {
			Enabled    bool   `json:"enabled"`
			ExpiresAt  int64  `json:"expires_at"`
			MaxDevices int    `json:"max_devices"`
			QuotaBytes int64  `json:"quota_bytes"`
			Note       string `json:"note"`
		}
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "参数错误")
			return
		}
		rec := vpn.AccessRecord{
			UUID:       uuid,
			Enabled:    body.Enabled,
			ExpiresAt:  unixOrZero(body.ExpiresAt),
			MaxDevices: body.MaxDevices,
			QuotaBytes: body.QuotaBytes,
			Note:       strings.TrimSpace(body.Note),
		}
		if err := st.UpsertAccess(r.Context(), rec, actorOf(r)); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.vpnAudit(r.Context(), actorOf(r), "vpn.access.update", uuid, "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// actorOf 取管理员用户名（用于审计）。
func actorOf(r *http.Request) string {
	if v := r.Context().Value(adminActorKey{}); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "admin"
}

// 占位：认证中间件未注入用户名时保持兼容。
type adminActorKey struct{}

// normalizeNodeDomain 规范化节点域名（用于证书 CN/SAN 与客户端连接）。
func normalizeNodeDomain(s string) (string, error) {
	d := strings.ToLower(strings.TrimSpace(s))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimPrefix(d, "http://")
	if i := strings.IndexByte(d, '/'); i >= 0 {
		d = d[:i]
	}
	if d == "" {
		return "", fmt.Errorf("domain 必填（用于节点证书与客户端连接）")
	}
	if len(d) > 255 || !strings.Contains(d, ".") || strings.ContainsAny(d, " :/") {
		return "", fmt.Errorf("domain 不合法：%s", d)
	}
	return d, nil
}
