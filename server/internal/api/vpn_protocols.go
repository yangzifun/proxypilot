package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// 本文件提供「节点协议能力 + 连接协议选择」的控制面逻辑：
//
//   - 节点声明支持哪些数据面协议（默认私有协议 private，可启用 MASQUE）；
//   - 门户连接接口按请求的 protocol 选择数据面，只允许节点已启用且就绪的协议；
//   - 连接会话记录本次使用的协议（vpn_sessions.protocol），便于审计与统计；
//   - 数据面本身由 tunnel / Agent 实现（并行任务），面板只负责契约与状态。
//
// 表结构见 migrations/0058_vpn_protocols.sql。

// 协议 id：与客户端、文档约定一致。
const (
	vpnProtocolPrivate = "private"
	vpnProtocolMasque  = "masque"
)

// MASQUE 状态：disabled=节点未启用；pending=已启用但数据面未就绪；
// ready=节点已上报就绪，客户端可连接；error=数据面异常。
const (
	masqueStateDisabled = "disabled"
	masqueStatePending  = "pending"
	masqueStateReady    = "ready"
	masqueStateError    = "error"

	// defaultMasquePath 是标准 CONNECT-IP 路径（RFC 9484 §3），与客户端内置默认值一致；
	// 节点实现（tunnel/node）只接受 /.well-known/masque/ip/ 前缀，管理端校验同此。
	defaultMasquePath = "/.well-known/masque/ip/0.0.0.0/0/"
	masquePathPrefix  = "/.well-known/masque/ip/"
)

// MASQUE 鉴权方式。
const (
	masqueAuthNone   = "none"
	masqueAuthBearer = "bearer"
)

// vpnNodeProtocols 是 vpn_nodes 上的协议能力列。
type vpnNodeProtocols struct {
	PrivateEnabled bool   `json:"private_enabled"`
	MasqueEnabled  bool   `json:"masque_enabled"`
	MasquePath     string `json:"masque_path"`
	MasquePort     int    `json:"masque_port"`
	MasqueAuth     string `json:"masque_auth"`
	MasqueState    string `json:"masque_state"`
}

// vpnAdminNodeView 是管理端节点列表/详情的返回结构：在原有节点字段上追加协议能力。
// 协议字段同时以平铺字段与 protocols 数组返回，方便前端与旧调用方各取所需。
type vpnAdminNodeView struct {
	vpn.NodeRecord
	PrivateEnabled bool             `json:"private_enabled"`
	MasqueEnabled  bool             `json:"masque_enabled"`
	MasquePath     string           `json:"masque_path"`
	MasquePort     int              `json:"masque_port"`
	MasqueAuth     string           `json:"masque_auth"`
	MasqueState    string           `json:"masque_state"`
	Protocols      []map[string]any `json:"protocols"`
}

// adminNodeView 组合节点记录与协议能力。
func adminNodeView(n vpn.NodeRecord, p vpnNodeProtocols) vpnAdminNodeView {
	p = p.normalized()
	return vpnAdminNodeView{
		NodeRecord:     n,
		PrivateEnabled: p.PrivateEnabled,
		MasqueEnabled:  p.MasqueEnabled,
		MasquePath:     p.MasquePath,
		MasquePort:     p.MasquePort,
		MasqueAuth:     p.MasqueAuth,
		MasqueState:    p.MasqueState,
		Protocols:      p.protocolDetails(),
	}
}

// defaultNodeProtocols 是旧数据/旧库的默认能力：仅启用私有协议。
func defaultNodeProtocols() vpnNodeProtocols {
	return vpnNodeProtocols{
		PrivateEnabled: true,
		MasqueEnabled:  false,
		MasquePath:     defaultMasquePath,
		MasqueAuth:     masqueAuthNone,
		MasqueState:    masqueStateDisabled,
	}
}

// normalized 补齐协议能力的零值，并保证 MASQUE 状态与开关一致。
func (p vpnNodeProtocols) normalized() vpnNodeProtocols {
	if strings.TrimSpace(p.MasquePath) == "" {
		p.MasquePath = defaultMasquePath
	}
	if p.MasqueAuth == "" {
		p.MasqueAuth = masqueAuthNone
	}
	switch p.MasqueState {
	case masqueStateDisabled, masqueStatePending, masqueStateReady, masqueStateError:
	default:
		p.MasqueState = masqueStateDisabled
	}
	if !p.MasqueEnabled {
		p.MasqueState = masqueStateDisabled
	} else if p.MasqueState == masqueStateDisabled {
		p.MasqueState = masqueStatePending
	}
	return p
}

// protocolIDs 返回节点可用协议的字符串数组（客户端 task-32 契约：protocols 为字符串数组）。
// 只列出已启用的协议；两者都关闭属于异常数据，安全兜底为 private。
func (p vpnNodeProtocols) protocolIDs() []string {
	p = p.normalized()
	out := make([]string, 0, 2)
	if p.PrivateEnabled {
		out = append(out, vpnProtocolPrivate)
	}
	if p.MasqueEnabled {
		out = append(out, vpnProtocolMasque)
	}
	if len(out) == 0 {
		out = append(out, vpnProtocolPrivate)
	}
	return out
}

// protocolDetails 生成协议明细数组：private 固定在前，MASQUE 附状态与接入参数。
func (p vpnNodeProtocols) protocolDetails() []map[string]any {
	p = p.normalized()
	if !p.PrivateEnabled && !p.MasqueEnabled {
		p.PrivateEnabled = true
	}
	out := make([]map[string]any, 0, 2)
	out = append(out, map[string]any{"id": vpnProtocolPrivate, "enabled": p.PrivateEnabled, "default": true})
	out = append(out, map[string]any{
		"id":      vpnProtocolMasque,
		"enabled": p.MasqueEnabled,
		"state":   p.MasqueState,
		"path":    p.MasquePath,
		"port":    p.MasquePort,
		"auth":    p.MasqueAuth,
	})
	return out
}

// parseVPNProtocol 解析请求协议：空值按私有协议（向后兼容旧客户端）。
func parseVPNProtocol(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", vpnProtocolPrivate:
		return vpnProtocolPrivate, nil
	case vpnProtocolMasque:
		return vpnProtocolMasque, nil
	default:
		return "", fmt.Errorf("不支持的协议 %q（可选：private、masque）", raw)
	}
}

// vpnProtocolError 是可返回给客户端的协议不可用错误。
type vpnProtocolError struct {
	Status  int
	Code    string
	Message string
}

func (e *vpnProtocolError) Error() string { return e.Message }

// resolveConnectProtocol 依据节点能力与请求协议决定实际使用的协议。
// 规则：只允许节点已启用的协议；MASQUE 还必须处于 ready（数据面就绪）。
func resolveConnectProtocol(node vpnNodeProtocols, requested string) (string, error) {
	proto, err := parseVPNProtocol(requested)
	if err != nil {
		return "", &vpnProtocolError{Status: 400, Code: "protocol_invalid", Message: err.Error()}
	}
	p := node.normalized()
	switch proto {
	case vpnProtocolPrivate:
		if !p.PrivateEnabled {
			return "", &vpnProtocolError{
				Status: 409, Code: "protocol_not_enabled",
				Message: "该节点未启用私有协议（private）",
			}
		}
	case vpnProtocolMasque:
		if !p.MasqueEnabled {
			return "", &vpnProtocolError{
				Status: 409, Code: "protocol_not_enabled",
				Message: "该节点未启用 MASQUE 协议",
			}
		}
		if p.MasqueState != masqueStateReady {
			return "", &vpnProtocolError{
				Status: 409, Code: "protocol_not_ready",
				Message: fmt.Sprintf("该节点 MASQUE 尚未就绪（当前状态：%s），暂不能连接；请先部署节点或等待节点上报就绪", p.MasqueState),
			}
		}
	}
	return proto, nil
}

// writeVPNProtocolError 输出协议错误；非协议错误按 500 兜底。
func writeVPNProtocolError(w http.ResponseWriter, err error) {
	var pe *vpnProtocolError
	if errors.As(err, &pe) {
		writeJSON(w, pe.Status, map[string]any{"error": pe.Message, "code": pe.Code})
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

// masqueEndpoint 返回客户端连接 MASQUE 数据面的 host:port。
func masqueEndpoint(node *vpn.NodeRecord, p vpnNodeProtocols) string {
	if node == nil {
		return ""
	}
	host := strings.TrimSpace(node.Domain)
	if host == "" {
		host, _, _ = net.SplitHostPort(node.Endpoint)
		if host == "" {
			host = strings.TrimSpace(node.Endpoint)
		}
	}
	port := p.MasquePort
	if port <= 0 {
		port = node.ListenPort
	}
	if port <= 0 {
		port = 443
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

// masqueServerName 返回 MASQUE TLS ServerName（优先节点域名，否则接入地址 host）。
func masqueServerName(node *vpn.NodeRecord) string {
	if node == nil {
		return ""
	}
	if d := strings.TrimSpace(node.Domain); d != "" {
		return d
	}
	host, _, _ := net.SplitHostPort(node.Endpoint)
	return host
}

// validateNodeProtocols 校验管理端提交的协议能力。
//
// 注意：显式提交的非法值必须先拒绝，再做 normalized 兜底 —— 否则
// normalized 会把未知状态/鉴权「悄悄修正」，管理端就收不到错误提示了。
func validateNodeProtocols(p vpnNodeProtocols) (vpnNodeProtocols, error) {
	if p.MasqueAuth != "" && p.MasqueAuth != masqueAuthNone && p.MasqueAuth != masqueAuthBearer {
		return p, fmt.Errorf("MASQUE 鉴权只能是 none 或 bearer")
	}
	if p.MasqueState != "" {
		switch p.MasqueState {
		case masqueStateDisabled, masqueStatePending, masqueStateReady, masqueStateError:
		default:
			return p, fmt.Errorf("MASQUE 状态只能是 disabled、pending、ready 或 error")
		}
	}
	p = p.normalized()
	if !p.PrivateEnabled && !p.MasqueEnabled {
		return p, fmt.Errorf("至少需要启用一种协议（private 或 masque）")
	}
	if !strings.HasPrefix(p.MasquePath, masquePathPrefix) {
		return p, fmt.Errorf("MASQUE 路径必须以 %s 开头（RFC 9484 CONNECT-IP 标准路径，例如 %s），当前为 %q",
			masquePathPrefix, defaultMasquePath, p.MasquePath)
	}
	if len(p.MasquePath) > 255 {
		return p, fmt.Errorf("MASQUE 路径过长（最多 255 字符）")
	}
	if strings.ContainsAny(p.MasquePath, " \t\r\n") {
		return p, fmt.Errorf("MASQUE 路径不能包含空白字符")
	}
	if p.MasquePort < 0 || p.MasquePort > 65535 {
		return p, fmt.Errorf("MASQUE 端口必须在 0-65535 之间（0 = 沿用监听端口）")
	}
	return p, nil
}

// loadNodeProtocols 读取节点协议能力；列缺失（旧库未迁移）时按默认 private 协议兜底。
func (h *handlers) loadNodeProtocols(ctx context.Context, nodeID int64) vpnNodeProtocols {
	if h.db == nil || nodeID <= 0 {
		return defaultNodeProtocols()
	}
	var priv, masq int
	var p vpnNodeProtocols
	err := h.db.QueryRowContext(ctx,
		`SELECT private_enabled, masque_enabled, masque_path, masque_port, masque_auth, masque_state
		   FROM vpn_nodes WHERE id = ?`, nodeID).
		Scan(&priv, &masq, &p.MasquePath, &p.MasquePort, &p.MasqueAuth, &p.MasqueState)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && h.log != nil {
			h.log.Warn("读取节点协议能力失败，按默认 private 协议兜底", "node_id", nodeID, "err", err)
		}
		return defaultNodeProtocols()
	}
	p.PrivateEnabled, p.MasqueEnabled = priv == 1, masq == 1
	return p.normalized()
}

// loadNodeProtocolsBatch 批量读取节点协议能力（列表页避免 N+1 查询）。
func (h *handlers) loadNodeProtocolsBatch(ctx context.Context, ids []int64) map[int64]vpnNodeProtocols {
	out := make(map[int64]vpnNodeProtocols, len(ids))
	if h.db == nil || len(ids) == 0 {
		for _, id := range ids {
			out[id] = defaultNodeProtocols()
		}
		return out
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, private_enabled, masque_enabled, masque_path, masque_port, masque_auth, masque_state
		   FROM vpn_nodes WHERE id IN (`+placeholders+`)`, args...)
	if err != nil {
		if h.log != nil {
			h.log.Warn("批量读取节点协议能力失败，按默认 private 协议兜底", "count", len(ids), "err", err)
		}
		for _, id := range ids {
			out[id] = defaultNodeProtocols()
		}
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var priv, masq int
		var p vpnNodeProtocols
		if err := rows.Scan(&id, &priv, &masq, &p.MasquePath, &p.MasquePort, &p.MasqueAuth, &p.MasqueState); err != nil {
			continue
		}
		p.PrivateEnabled, p.MasqueEnabled = priv == 1, masq == 1
		out[id] = p.normalized()
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			out[id] = defaultNodeProtocols()
		}
	}
	return out
}

// applyNodeProtocols 写入节点协议能力（节点创建/更新后调用）。
func (h *handlers) applyNodeProtocols(ctx context.Context, nodeID int64, p vpnNodeProtocols) error {
	if h.db == nil || nodeID <= 0 {
		return nil
	}
	p = p.normalized()
	priv, masq := 0, 0
	if p.PrivateEnabled {
		priv = 1
	}
	if p.MasqueEnabled {
		masq = 1
	}
	_, err := h.db.ExecContext(ctx,
		`UPDATE vpn_nodes SET private_enabled = ?, masque_enabled = ?, masque_path = ?,
		   masque_port = ?, masque_auth = ?, masque_state = ? WHERE id = ?`,
		priv, masq, p.MasquePath, p.MasquePort, p.MasqueAuth, p.MasqueState, nodeID)
	if err != nil {
		return fmt.Errorf("写入节点协议能力失败: %w", err)
	}
	return nil
}

// markMasquePending 在节点重新部署后把 MASQUE 重置为「等待节点就绪」。
// 部署会重启节点数据面，之前的 ready 状态不再可信，因此无条件回到 pending。
func (h *handlers) markMasquePending(ctx context.Context, nodeID int64) error {
	if h.db == nil || nodeID <= 0 {
		return nil
	}
	_, err := h.db.ExecContext(ctx,
		`UPDATE vpn_nodes SET masque_state = ? WHERE id = ? AND masque_enabled = 1`,
		masqueStatePending, nodeID)
	return err
}

// updateSessionProtocol 把本次连接使用的协议写入会话记录。
func (h *handlers) updateSessionProtocol(ctx context.Context, sessionID int64, protocol string) error {
	if h.db == nil || sessionID <= 0 {
		return nil
	}
	if protocol != vpnProtocolMasque {
		protocol = vpnProtocolPrivate
	}
	_, err := h.db.ExecContext(ctx,
		`UPDATE vpn_sessions SET protocol = ? WHERE id = ?`, protocol, sessionID)
	if err != nil {
		return fmt.Errorf("记录会话协议失败: %w", err)
	}
	return nil
}
