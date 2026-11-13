package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

// =====================================================================
//  UUID 更换（轮换）
//
//  产品语义：换的是「订阅 UUID」，不是用户。uuid_info 的 id / 邮箱 / 密码 /
//  状态 / 备注全部保留，只把 uuid 列换掉；旧 UUID 的配置、授权、历史数据整体
//  迁移到新 UUID，旧 /sub/{old} 等链接立即查不到配置；节点授权列表同步替换并在
//  事务提交后重渲染下发，保证节点侧旧 UUID 立刻不能再用。
//
//  管理端：POST /api/uuids/{uuid}/rotate
//    body {new_uuid?, confirm?}（new_uuid 为空则生成 UUIDv4；confirm 传旧 uuid 做二次确认）
//  用户端：POST /api/portal/uuid/rotate
//    body {password?, code?, new_uuid?}
//    有密码必须验当前密码；无密码必须验邮箱验证码（不能只凭 portal token）。
//  用户端发码：POST /api/portal/uuid/rotate/send-code
//    body {cf_turnstile_response?}，向绑定邮箱发 6 位验证码（purpose 独立）。
// =====================================================================

// portalUUIDRotatePurpose 用户端「更换 UUID」的邮箱验证码用途。
// 与登录（portal_login）、改邮箱（portal_email_change）隔离，互不通用。
const portalUUIDRotatePurpose = "portal_uuid_rotate"

// uuidRotateError 带 HTTP 状态码的轮换错误（400/404/409/500）。
type uuidRotateError struct {
	Status int
	Msg    string
}

func (e *uuidRotateError) Error() string { return e.Msg }

func rotateErr(status int, msg string) *uuidRotateError {
	return &uuidRotateError{Status: status, Msg: msg}
}

// generateUUIDv4 生成标准 UUIDv4（crypto/rand，版本/变体位按 RFC 4122 设置）。
func generateUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// normalizeUUIDForRotate 统一小写去空格；数据库里的 UUID 均为小写。
func normalizeUUIDForRotate(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// validateUUIDRotateInput 校验旧/新 UUID 与二次确认，返回规范化后的 (old, new)。
// newRaw 为空时生成 UUIDv4。纯函数，便于单测。
func validateUUIDRotateInput(oldRaw, newRaw, confirm string) (string, string, *uuidRotateError) {
	oldUUID := normalizeUUIDForRotate(oldRaw)
	if !uuidRe.MatchString(oldUUID) {
		return "", "", rotateErr(http.StatusBadRequest, "UUID格式无效")
	}
	newUUID := normalizeUUIDForRotate(newRaw)
	if newUUID == "" {
		gen, err := generateUUIDv4()
		if err != nil {
			return "", "", rotateErr(http.StatusInternalServerError, "生成新 UUID 失败")
		}
		newUUID = gen
	}
	if !uuidRe.MatchString(newUUID) {
		return "", "", rotateErr(http.StatusBadRequest, "新 UUID 格式无效")
	}
	if newUUID == oldUUID {
		return "", "", rotateErr(http.StatusBadRequest, "新 UUID 不能与旧 UUID 相同")
	}
	if c := strings.TrimSpace(confirm); c != "" && !strings.EqualFold(c, oldUUID) {
		return "", "", rotateErr(http.StatusBadRequest, "确认 UUID 与原 UUID 不一致")
	}
	return oldUUID, newUUID, nil
}

// replaceUUIDListJSON 把 JSON 数组里的旧 UUID 替换为新 UUID（大小写不敏感）并去重。
// 返回 (新 JSON, 是否有变化, 错误)；无变化时原样返回，保持已有格式。
func replaceUUIDListJSON(raw, oldUUID, newUUID string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return raw, false, nil
	}
	var arr []any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return "", false, err
	}
	out := make([]string, 0, len(arr))
	seen := make(map[string]bool, len(arr))
	changed := false
	for _, v := range arr {
		u := strings.TrimSpace(jsonStr(v))
		if u == "" {
			continue
		}
		if strings.EqualFold(u, oldUUID) {
			u = newUUID
			changed = true
		}
		key := strings.ToLower(u)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, u)
	}
	if !changed {
		return raw, false, nil
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// uuidRotateColumnTables 列名统一为 `uuid`、需要整体迁移的关联表。
// 节点授权列表（singbox_nodes / mihomo_nodes 的 uuid_list JSON）、configs
// （还要替换 config_data 内嵌 UUID）、uuid_info、vpn_node_grants（subject_id）
// 由于 SQL 特殊，在 rotateUUID 里单独处理。
var uuidRotateColumnTables = []string{
	"config_access_logs",
	"masque_warp_profiles",
	"mihomo_traffic",
	"mihomo_traffic_hourly",
	"mtproto_clients",
	"node_configs",
	"node_user_counter",
	"user_node_recent",
	"user_traffic",
	"user_traffic_hourly",
	"uuid_port_map",
	"uuid_sub_sources",
	"vpn_device_codes",
	"vpn_devices",
	"vpn_user_access",
}

// isMissingTableError 识别「表不存在」（老库未跑到对应迁移）：这类表直接跳过。
func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "1146") ||
		strings.Contains(msg, "no such table")
}

// rowQueryer 同时兼容 *sql.DB 与 *sql.Tx。
type rowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// uuidRotateConflict 检查新 UUID 是否已在任一迁移目标表里存在（有则 409）。
// 返回冲突表名；空字符串表示无冲突。
func (h *handlers) uuidRotateConflict(ctx context.Context, q rowQueryer, newUUID string) (string, error) {
	tables := append([]string{"configs", "uuid_info"}, uuidRotateColumnTables...)
	for _, table := range tables {
		var n int64
		err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"` WHERE uuid = ?", newUUID).Scan(&n)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) || isMissingTableError(err) {
				continue
			}
			return "", err
		}
		if n > 0 {
			return table, nil
		}
	}
	var n int64
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM vpn_node_grants WHERE subject_id = ? AND subject_type = 'user'", newUUID).Scan(&n)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !isMissingTableError(err) {
			return "", err
		}
	} else if n > 0 {
		return "vpn_node_grants", nil
	}
	return "", nil
}

// uuidRotateCounts 一次轮换的统计（用于响应与审计）。
type uuidRotateCounts struct {
	ConfigsUpdated int64
	TablesUpdated  int64
	NodesUpdated   int64
	NodesDeployed  int64
	Warnings       []string
	// 事务内收集，提交后用于重渲染下发
	NodeIDs    []int64
	NextByNode map[int64][]string
}

// rotateUUIDService 执行 UUID 轮换：
//  1. 事务内：校验旧 UUID 存在、新 UUID 不冲突 → 迁移所有 UUID 列 / config_data /
//     节点授权列表 → 最后更新 uuid_info.uuid；任一步失败回滚。
//  2. 事务提交后：对受影响的 sing-box 节点重渲染、同步订阅配置并排队下发；
//     个别节点失败不阻断，汇总为 warning。
func (h *handlers) rotateUUIDService(ctx context.Context, oldUUID, newUUID, actor string) (*uuidRotateCounts, *uuidRotateError) {
	now := nowMillis()
	counts := &uuidRotateCounts{NextByNode: map[int64][]string{}}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		h.log.Error("UUID 轮换：开启事务失败", "err", err, "old_uuid", oldUUID)
		return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
	}
	defer tx.Rollback()

	// 1) 旧 UUID 必须存在：uuid_info 行优先（保留其 id/邮箱/密码），else 至少要有配置。
	var infoID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM uuid_info WHERE uuid = ? FOR UPDATE", oldUUID).Scan(&infoID)
	hasInfo := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTableError(err) {
		h.log.Error("UUID 轮换：查询旧 UUID 失败", "err", err, "old_uuid", oldUUID)
		return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
	}
	var oldConfigs int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM configs WHERE uuid = ?", oldUUID).Scan(&oldConfigs); err != nil {
		if !errors.Is(err, sql.ErrNoRows) && !isMissingTableError(err) {
			h.log.Error("UUID 轮换：统计旧配置失败", "err", err, "old_uuid", oldUUID)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
	}
	if !hasInfo && oldConfigs == 0 {
		return nil, rotateErr(http.StatusNotFound, "UUID 不存在")
	}

	// 2) 新 UUID 冲突预检（uuid_info/configs/各关联表），提前 409。
	if conflictTable, err := h.uuidRotateConflict(ctx, tx, newUUID); err != nil {
		h.log.Error("UUID 轮换：冲突预检失败", "err", err, "new_uuid", newUUID)
		return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
	} else if conflictTable != "" {
		return nil, rotateErr(http.StatusConflict, "新 UUID 已存在（冲突表："+conflictTable+"），请换一个")
	}

	// 3) configs：uuid 列 + config_data 内嵌 UUID（小写与大写变体）一起替换。
	res, err := tx.ExecContext(ctx,
		"UPDATE configs SET uuid = ?, config_data = REPLACE(REPLACE(config_data, ?, ?), ?, ?), updated_at = ? WHERE uuid = ?",
		newUUID, oldUUID, newUUID, strings.ToUpper(oldUUID), strings.ToUpper(newUUID), now, oldUUID)
	if err != nil {
		if isDuplicateKeyError(err) {
			return nil, rotateErr(http.StatusConflict, "新 UUID 已存在（configs 冲突）")
		}
		h.log.Error("UUID 轮换：迁移 configs 失败", "err", err)
		return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
	}
	if n, _ := res.RowsAffected(); n > 0 {
		counts.ConfigsUpdated = n
		counts.TablesUpdated++
	}

	// 4) VPN 会话：旧 UUID 名下设备的活动会话立即结束（vpn_sessions 无 uuid 列，
	//    必须在 vpn_devices.uuid 迁移之前按旧值关联）。
	if res, err := tx.ExecContext(ctx,
		`UPDATE vpn_sessions SET ended_at = ?, close_reason = 'uuid_rotated'
		  WHERE ended_at = 0 AND device_id IN (SELECT id FROM vpn_devices WHERE uuid = ?)`,
		now, oldUUID); err != nil {
		if !isMissingTableError(err) {
			h.log.Error("UUID 轮换：结束 VPN 会话失败", "err", err)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
	} else if n, _ := res.RowsAffected(); n > 0 {
		counts.TablesUpdated++
	}

	// 5) 统一 uuid 列的关联表（表不存在 = 该功能未启用，跳过）。
	for _, table := range uuidRotateColumnTables {
		res, err := tx.ExecContext(ctx, "UPDATE `"+table+"` SET uuid = ? WHERE uuid = ?", newUUID, oldUUID)
		if err != nil {
			if isMissingTableError(err) {
				continue
			}
			if isDuplicateKeyError(err) {
				return nil, rotateErr(http.StatusConflict, "新 UUID 已存在（冲突表："+table+"）")
			}
			h.log.Error("UUID 轮换：迁移表失败", "err", err, "table", table)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
		if n, _ := res.RowsAffected(); n > 0 {
			counts.TablesUpdated++
		}
	}

	// 6) VPN 节点授权：subject_type=user 的 subject_id 就是 UUID。
	if res, err := tx.ExecContext(ctx,
		`UPDATE vpn_node_grants SET subject_id = ? WHERE subject_id = ? AND subject_type = 'user'`,
		newUUID, oldUUID); err != nil {
		if !isMissingTableError(err) {
			h.log.Error("UUID 轮换：迁移 VPN 节点授权失败", "err", err)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
	} else if n, _ := res.RowsAffected(); n > 0 {
		counts.TablesUpdated++
	}

	// 7) sing-box 节点授权列表：JSON 数组里旧→新（提交后对这些节点重渲染下发）。
	if nodeIDs, next, err := h.rotateUUIDListTable(ctx, tx, "singbox_nodes", oldUUID, newUUID, now); err != nil {
		if !isMissingTableError(err) {
			h.log.Error("UUID 轮换：迁移 sing-box 授权列表失败", "err", err)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
	} else if len(nodeIDs) > 0 {
		counts.NodesUpdated = int64(len(nodeIDs))
		counts.NodeIDs = nodeIDs
		counts.NextByNode = next
		counts.TablesUpdated++
	}

	// 8) 遗留 mihomo 节点授权列表（只换 JSON，由现有对账/下发流程处理节点侧）。
	if nodeIDs, _, err := h.rotateUUIDListTable(ctx, tx, "mihomo_nodes", oldUUID, newUUID, now); err != nil {
		if !isMissingTableError(err) {
			h.log.Error("UUID 轮换：迁移 mihomo 授权列表失败", "err", err)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
	} else if len(nodeIDs) > 0 {
		counts.TablesUpdated++
	}

	// 9) 最后更新 uuid_info.uuid（保留同一 id / 邮箱 / 密码 / 状态 / 备注）。
	if hasInfo {
		res, err := tx.ExecContext(ctx, "UPDATE uuid_info SET uuid = ?, updated_at = ? WHERE uuid = ?", newUUID, now, oldUUID)
		if err != nil {
			if isDuplicateKeyError(err) {
				return nil, rotateErr(http.StatusConflict, "新 UUID 已存在（uuid_info 冲突）")
			}
			h.log.Error("UUID 轮换：更新 uuid_info 失败", "err", err)
			return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			// 并发：另一个请求已经把旧 UUID 换走。
			return nil, rotateErr(http.StatusConflict, "旧 UUID 已被更换，请刷新后重试")
		}
		counts.TablesUpdated++
	}

	if err := tx.Commit(); err != nil {
		h.log.Error("UUID 轮换：提交事务失败", "err", err)
		return nil, rotateErr(http.StatusInternalServerError, "更换失败，请稍后重试")
	}

	// 10) 提交后：节点重渲染 + 订阅配置同步 + 排队下发（失败不阻断，汇总 warning）。
	if len(counts.NodeIDs) > 0 {
		deployed, warns := h.syncRotatedUUIDNodes(ctx, counts.NodeIDs, counts.NextByNode, actor)
		counts.NodesDeployed = int64(deployed)
		counts.Warnings = warns
	}

	h.log.Info("UUID 已更换",
		"actor", actor, "old_uuid", oldUUID, "new_uuid", newUUID,
		"configs_updated", counts.ConfigsUpdated, "tables_updated", counts.TablesUpdated,
		"nodes_updated", counts.NodesUpdated, "nodes_deployed", counts.NodesDeployed)
	return counts, nil
}

// rotateUUIDListTable 迁移某张节点表的 uuid_list JSON；返回受影响的节点 id 与新列表。
func (h *handlers) rotateUUIDListTable(ctx context.Context, tx *sql.Tx, table, oldUUID, newUUID string, now int64) ([]int64, map[int64][]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id, COALESCE(uuid_list,'[]') FROM `"+table+"`")
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	type pending struct {
		id  int64
		raw string
	}
	var affected []pending
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, nil, err
		}
		nextRaw, changed, jerr := replaceUUIDListJSON(raw, oldUUID, newUUID)
		if jerr != nil {
			// 授权列表格式异常：不能静默跳过，否则旧 UUID 可能仍留在节点上。
			return nil, nil, fmt.Errorf("%s 节点 #%d 授权列表格式异常：%v", table, id, jerr)
		}
		if !changed {
			continue
		}
		affected = append(affected, pending{id: id, raw: nextRaw})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	ids := make([]int64, 0, len(affected))
	next := make(map[int64][]string, len(affected))
	for _, p := range affected {
		if _, err := tx.ExecContext(ctx,
			"UPDATE `"+table+"` SET uuid_list = ?, updated_at = ? WHERE id = ?", p.raw, now, p.id); err != nil {
			return nil, nil, err
		}
		ids = append(ids, p.id)
		next[p.id] = parseUUIDList(p.raw)
	}
	return ids, next, nil
}

// syncRotatedUUIDNodes 事务提交后同步节点：重渲染 → 同步订阅配置 → 排队下发。
// 单节点失败只记 warning，不影响其它节点与接口成功。
func (h *handlers) syncRotatedUUIDNodes(ctx context.Context, nodeIDs []int64, nextByNode map[int64][]string, actor string) (int, []string) {
	deployed := 0
	warnings := make([]string, 0)
	for _, nodeID := range nodeIDs {
		tpl, node, _, _, msg := h.loadSingboxNodeFull(ctx, nodeID)
		if msg != "" {
			warnings = append(warnings, fmt.Sprintf("节点 #%d 重渲染失败：%s", nodeID, msg))
			continue
		}
		users := singboxUsersFromUUIDs(tpl, nextByNode[nodeID])
		files, _, _, _, applyMsg := h.applySingboxNode(ctx, nodeID, users, false)
		if applyMsg != "" {
			warnings = append(warnings, fmt.Sprintf("节点 #%d 重渲染失败：%s", nodeID, applyMsg))
			continue
		}
		// applySingboxNode 内部已同步一次；这里按「轮换」语义再显式同步，幂等兜底。
		if _, _, err := h.syncSingboxConfigsForUsers(ctx, nodeID, node.Name, tpl, node, users); err != nil {
			warnings = append(warnings, fmt.Sprintf("节点 #%d 订阅配置同步失败：%v", nodeID, err))
		}
		queued := 0
		for _, item := range h.queueSingboxDeploy(ctx, files, actor) {
			if ok, _ := item["ok"].(bool); ok {
				queued++
			} else if msg, _ := item["error"].(string); msg != "" {
				warnings = append(warnings, fmt.Sprintf("节点 #%d 下发排队失败：%s", nodeID, msg))
			}
		}
		if queued > 0 {
			deployed++
		}
		h.verifyNodeConsistencyAsync(nodeID)
	}
	return deployed, warnings
}

// uuidRotateSubLinks 生成新 UUID 的三条订阅链接。
func uuidRotateSubLinks(origin, uuid string) map[string]string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	return map[string]string{
		"raw":   origin + "/sub/" + url.PathEscape(uuid),
		"batch": origin + "/batch-configs/" + url.PathEscape(uuid),
		"clash": origin + "/clash-config?uuid=" + url.QueryEscape(uuid),
	}
}

// writeUUIDRotateResult 输出管理端/用户端统一的轮换响应。
func (h *handlers) writeUUIDRotateResult(w http.ResponseWriter, r *http.Request, oldUUID, newUUID, message string, counts *uuidRotateCounts) {
	body := map[string]any{
		"success":         true,
		"old_uuid":        oldUUID,
		"new_uuid":        newUUID,
		"configs_updated": counts.ConfigsUpdated,
		"tables_updated":  counts.TablesUpdated,
		"nodes_updated":   counts.NodesUpdated,
		"nodes_deployed":  counts.NodesDeployed,
		"sub":             uuidRotateSubLinks(requestOrigin(r), newUUID),
		"message":         message,
	}
	if len(counts.Warnings) > 0 {
		body["warning"] = strings.Join(counts.Warnings, "；")
	}
	writeJSON(w, http.StatusOK, body)
}

// auditUUIDRotate 记录一条轮换审计（管理端/用户端共用，actor 由调用方给出）。
func (h *handlers) auditUUIDRotate(ctx context.Context, actor, oldUUID, newUUID string, counts *uuidRotateCounts) {
	detail := fmt.Sprintf("configs=%d tables=%d nodes=%d deployed=%d",
		counts.ConfigsUpdated, counts.TablesUpdated, counts.NodesUpdated, counts.NodesDeployed)
	if len(counts.Warnings) > 0 {
		detail += " warning=" + strings.Join(counts.Warnings, "|")
	}
	h.vpnAudit(ctx, actor, "uuid.rotate", oldUUID+"->"+newUUID, detail)
}

// POST /api/uuids/{uuid}/rotate —— 管理端更换 UUID
func (h *handlers) uuidRotateAdmin(w http.ResponseWriter, r *http.Request) {
	oldRaw := pathParam(r, "uuid")
	var req struct {
		NewUUID string `json:"new_uuid"`
		Confirm string `json:"confirm"`
	}
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	oldUUID, newUUID, verr := validateUUIDRotateInput(oldRaw, req.NewUUID, req.Confirm)
	if verr != nil {
		writeError(w, verr.Status, verr.Msg)
		return
	}
	actor := "admin"
	if c, ok := claimsFrom(r); ok && strings.TrimSpace(c.Sub) != "" {
		actor = c.Sub
	}
	counts, rerr := h.rotateUUIDService(r.Context(), oldUUID, newUUID, actor)
	if rerr != nil {
		h.log.Warn("UUID 更换失败", "actor", actor, "old_uuid", oldUUID, "new_uuid", newUUID, "err", rerr.Msg)
		writeError(w, rerr.Status, rerr.Msg)
		return
	}
	h.auditUUIDRotate(r.Context(), actor, oldUUID, newUUID, counts)
	h.writeUUIDRotateResult(w, r, oldUUID, newUUID,
		"UUID 已更换：旧 UUID 立即失效，请使用新订阅链接", counts)
}

// POST /api/portal/uuid/rotate —— 用户端更换 UUID（必须二次证明：当前密码或邮箱验证码）
func (h *handlers) portalUUIDRotate(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	email = normalizeEmail(email)
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
		NewUUID  string `json:"new_uuid"`
	}
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	row, err := h.findPortalPasswordByEmail(r.Context(), email)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		h.log.Error("UUID 更换：查询用户失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	// 先做格式/二次确认校验（不消耗验证码、不访问迁移逻辑）。
	oldUUID, newUUID, verr := validateUUIDRotateInput(row.UUID, req.NewUUID, "")
	if verr != nil {
		writeError(w, verr.Status, verr.Msg)
		return
	}

	// 二次认证：有密码必须验密码；无密码必须验邮箱验证码。只凭 portal token 一律拒绝。
	// 注意：这里 portal token 本身是有效的，失败的是「二次证明」，所以密码缺失/错误返回
	// 400（与 /api/portal/password 的「原密码不正确」一致），只有 token 失效才返回 401，
	// 避免前端把「密码输错」误判成登录过期而清掉登录态。
	if strings.TrimSpace(row.Hash) != "" {
		if req.Password == "" {
			writeError(w, http.StatusBadRequest, "请输入当前密码后再更换 UUID")
			return
		}
		key := loginRateKey(r, "portal_uuid_rotate:"+email)
		if wait, blocked := loginLimiter.Blocked(key); blocked {
			w.Header().Set("Retry-After", retryAfterSeconds(wait))
			writeError(w, http.StatusTooManyRequests, "尝试过于频繁，请 "+retryAfterSeconds(wait)+" 秒后再试")
			return
		}
		if ok, _ := auth.VerifyPassword(row.Hash, req.Password); !ok {
			writeError(w, http.StatusBadRequest, "当前密码不正确")
			return
		}
		loginLimiter.Reset(key)
	} else {
		code := strings.TrimSpace(req.Code)
		if len(code) != 6 || !isDigits(code) {
			writeError(w, http.StatusBadRequest, "请输入邮箱验证码后再更换 UUID")
			return
		}
		if ok, status, msg := h.consumeEmailCode(r.Context(), email, code, portalUUIDRotatePurpose); !ok {
			writeError(w, status, msg)
			return
		}
	}

	actor := "portal:" + emailNormForLog(email)
	counts, rerr := h.rotateUUIDService(r.Context(), oldUUID, newUUID, actor)
	if rerr != nil {
		h.log.Warn("用户端 UUID 更换失败", "actor", actor, "old_uuid", oldUUID, "new_uuid", newUUID, "err", rerr.Msg)
		writeError(w, rerr.Status, rerr.Msg)
		return
	}
	h.auditUUIDRotate(r.Context(), actor, oldUUID, newUUID, counts)
	h.writeUUIDRotateResult(w, r, oldUUID, newUUID,
		"UUID 已更换：旧订阅链接已失效，请使用新的订阅链接", counts)
}

// POST /api/portal/uuid/rotate/send-code —— 向绑定邮箱发送更换 UUID 的验证码
func (h *handlers) portalUUIDRotateSendCode(w http.ResponseWriter, r *http.Request) {
	email, ok := h.portalSession(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	var req struct {
		Turnstile string `json:"cf_turnstile_response"`
	}
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if err := h.verifyTurnstile(r, req.Turnstile); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	binding, found := h.bindingByEmail(r.Context(), normalizeEmail(email))
	if !found {
		writeError(w, http.StatusNotFound, "当前账号还没有绑定 UUID，请联系管理员")
		return
	}
	row, err := h.findPortalPasswordByEmail(r.Context(), normalizeEmail(email))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if strings.TrimSpace(row.Hash) != "" {
		writeError(w, http.StatusBadRequest, "该账号已设置密码，请使用当前密码确认更换")
		return
	}
	target := normalizeEmail(binding.Email)
	if target == "" {
		writeError(w, http.StatusNotFound, "账号未绑定邮箱，无法发送验证码")
		return
	}
	if code, msg := h.checkEmailCodeRate(r.Context(), target, portalUUIDRotatePurpose); code != 0 {
		writeError(w, code, msg)
		return
	}
	code, err := h.issueEmailCode(r.Context(), target, target, portalUUIDRotatePurpose)
	if err != nil {
		h.log.Error("UUID 更换：写入验证码失败", "err", err)
		writeError(w, http.StatusInternalServerError, "验证码发送失败，请稍后重试")
		return
	}
	ttl := int(emailCodeTTL.Minutes())
	if err := h.sendMail(target, "【ProxyPilot】更换订阅 UUID 验证码",
		emailHTML("更换订阅 UUID", "您正在更换订阅 UUID，旧订阅链接将立即失效。请输入下面的 6 位验证码完成确认：", code, ttl)); err != nil {
		if errors.Is(err, errMailNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		h.log.Warn("UUID 更换验证码邮件发送失败", "err", err)
		writeError(w, http.StatusBadGateway, "验证码邮件发送失败，请稍后重试")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"email":      target,
		"expires_in": int(emailCodeTTL.Seconds()),
		"message":    "验证码已发送到绑定邮箱",
	})
}
