package api

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
)

// serverAuthorizedUUIDs 返回该服务器上已授权的 UUID 集合：
// sing-box 节点的 uuid_list ∪ MTProto 节点 active 客户端。excludeNodeID 用于排除正在处理的节点自身。
func (h *handlers) serverAuthorizedUUIDs(ctx context.Context, serverID, excludeSingboxID, excludeMtprotoID int64) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(u string) {
		s := strings.ToLower(strings.TrimSpace(u))
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	if rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, COALESCE(uuid_list,'[]') AS uuid_list FROM singbox_nodes WHERE server_id = ?`, serverID); err == nil {
		for _, row := range rows {
			if excludeSingboxID > 0 && mapInt(row, "id", 0) == excludeSingboxID {
				continue
			}
			for _, u := range parseUUIDList(mapStr(row, "uuid_list", "[]")) {
				add(u)
			}
		}
	}
	if rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT c.uuid, c.node_id FROM mtproto_clients c JOIN mtproto_nodes n ON n.id = c.node_id
		  WHERE n.server_id = ? AND c.status = 'active'`, serverID); err == nil {
		for _, row := range rows {
			if excludeMtprotoID > 0 && mapInt(row, "node_id", 0) == excludeMtprotoID {
				continue
			}
			add(mapStr(row, "uuid", ""))
		}
	}
	sort.Strings(out)
	return out
}

// inheritAuthorizationToSingboxNode 新建 sing-box 节点时继承该服务器已授权的 UUID，
// 立即渲染 + 同步订阅配置 + 排队下发（离线则等 Agent 上线后补下发），随后做一致性复核。
func (h *handlers) inheritAuthorizationToSingboxNode(ctx context.Context, nodeID int64) {
	var serverID int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(server_id,0) FROM singbox_nodes WHERE id = ? LIMIT 1`, nodeID).Scan(&serverID); err != nil {
		return
	}
	uuids := h.serverAuthorizedUUIDs(ctx, serverID, nodeID, 0)
	if len(uuids) == 0 {
		return
	}
	raw, err := json.Marshal(uuids)
	if err != nil {
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, raw, nowMillis(), nodeID); err != nil {
		return
	}
	files, _, _, _, msg := h.applySingboxNode(ctx, nodeID, nil, false)
	if msg == "" {
		h.queueSingboxDeploy(ctx, files, "node-inherit")
		if tpl, node, _, _, m := h.loadSingboxNodeFull(ctx, nodeID); m == "" {
			users := singboxUsersFromUUIDs(tpl, uuids)
			_, _, _ = h.syncSingboxConfigsForUsers(ctx, nodeID, node.Name, tpl, node, users)
		}
	}
	h.recordNodeLog(ctx, 0, serverID, "node-inherit", "success", 0,
		"新节点继承该服务器已授权 UUID "+itoa64(int64(len(uuids)))+" 个，并排队下发", "", "admin")
	h.verifyNodeConsistencyAsync(nodeID)
}

// inheritAuthorizationToMtprotoNode 新建 MTProto 节点时继承该服务器已授权的 UUID，
// 为每个 UUID 创建 active 客户端并重渲染下发。
func (h *handlers) inheritAuthorizationToMtprotoNode(ctx context.Context, nodeID int64) {
	rows, err := h.mtprotoNodesWithMeta(ctx, "WHERE n.id = ?", nodeID)
	if err != nil || len(rows) == 0 {
		return
	}
	serverID := mapInt(rows[0], "server_id", 0)
	uuids := h.serverAuthorizedUUIDs(ctx, serverID, 0, nodeID)
	if len(uuids) == 0 {
		return
	}
	tpl, _, _ := h.loadMtprotoTemplate(ctx, mapInt(rows[0], "template_id", 0))
	eff := mtprotoEffective(tpl, mtprotoNodeFromRow(rows[0]))
	secret := mtprotoGenerateSecret(eff.Domain)
	if secret == "" {
		h.recordNodeLog(ctx, 0, serverID, "node-inherit", "failed", 0,
			"新 MTProto 节点缺少有效伪装域名，无法继承授权 UUID（请补全域名后重新授权）", "", "admin")
		return
	}
	added := 0
	for _, u := range uuids {
		var active int64
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM mtproto_clients WHERE node_id = ? AND LOWER(uuid) = ? AND status = 'active'`, nodeID, u).Scan(&active)
		if active > 0 {
			continue
		}
		if _, err := h.db.ExecContext(ctx, `INSERT INTO mtproto_clients
		  (node_id, uuid, email, label, secret, status, remark, created_at, updated_at)
		  VALUES (?, ?, '', '', ?, 'active', '节点创建继承', ?, ?)`,
			nodeID, u, secret, nowMillis(), nowMillis()); err == nil {
			added++
		}
	}
	if _, _, msg := h.applyMtprotoToNode(ctx, nodeID); msg != "" {
		h.recordNodeLog(ctx, 0, serverID, "node-inherit", "failed", 0,
			"MTProto 节点继承授权后下发失败："+msg, "", "admin")
		return
	}
	h.recordNodeLog(ctx, 0, serverID, "node-inherit", "success", 0,
		"新 MTProto 节点继承该服务器已授权 UUID "+itoa64(int64(added))+" 个，并排队下发", "", "admin")
}
