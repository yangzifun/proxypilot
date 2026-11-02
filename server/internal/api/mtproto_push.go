package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func (h *handlers) ensureMtprotoNode(ctx context.Context, serverID int64) (int64, string, string) {
	var nodeID int64
	if err := h.db.QueryRowContext(ctx, `SELECT id FROM mtproto_nodes WHERE server_id = ? LIMIT 1`, serverID).Scan(&nodeID); err == nil {
		return nodeID, "", ""
	}
	var templateID int64
	_ = h.db.QueryRowContext(ctx, `SELECT id FROM mtproto_templates WHERE enabled = 1 ORDER BY id ASC LIMIT 1`).Scan(&templateID)
	if templateID == 0 {
		return 0, "", "没有可用的 MTProto 模板"
	}
	var serverName, host, domain string
	if err := h.db.QueryRowContext(ctx, `SELECT COALESCE(name,''), COALESCE(host,''), COALESCE(singbox_domain,'')
	      FROM servers WHERE id = ? LIMIT 1`, serverID).Scan(&serverName, &host, &domain); err != nil {
		return 0, "", "服务器不存在"
	}
	var tplDomain string
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(domain,'') FROM mtproto_templates WHERE id = ?`, templateID).Scan(&tplDomain)
	dom := firstNonEmpty(strings.TrimSpace(domain), strings.TrimSpace(host), tplDomain)
	secret := mtprotoGenerateSecret(dom)
	if secret == "" {
		return 0, "", "模板缺少有效伪装域名"
	}
	base := truncateRunes(serverName, 40)
	if base == "" {
		base = fmt.Sprintf("mtproto-%d", serverID)
	}
	name := base
	for i := 2; i < 50; i++ {
		var dup int64
		_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mtproto_nodes WHERE name = ?`, name).Scan(&dup)
		if dup == 0 {
			break
		}
		name = fmt.Sprintf("%s-%d", truncateRunes(base, 40), i)
	}
	res, err := h.db.ExecContext(ctx, `INSERT INTO mtproto_nodes
	  (server_id, template_id, name, domain, host, bootstrap_secret, enabled, remark, created_at, updated_at)
	  VALUES (?, ?, ?, ?, '', ?, 1, 'UUID 授权推送自动创建', ?, ?)`,
		serverID, templateID, name, dom, secret, nowMillis(), nowMillis())
	if err != nil {
		return 0, "", "创建节点失败：" + err.Error()
	}
	id, _ := res.LastInsertId()
	return id, name, ""
}

// POST /api/mtproto/push —— UUID × 服务器批量授权（生成/恢复客户端后下发）
func (h *handlers) mtprotoPush(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuids := []string{}
	for _, v := range jsonSlice(body["uuids"]) {
		if s := strings.TrimSpace(jsonStr(v)); s != "" && len(uuids) < 200 {
			uuids = append(uuids, s)
		}
	}
	serverIDs := []int64{}
	for _, v := range jsonSlice(body["server_ids"]) {
		if id := jsonInt(v); id > 0 && len(serverIDs) < 100 {
			serverIDs = append(serverIDs, id)
		}
	}
	if len(uuids) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一个 UUID")
		return
	}
	if len(serverIDs) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一台服务器")
		return
	}
	doPush := true
	if v, ok := body["push"].(bool); ok {
		doPush = v
	}

	out := make([]map[string]any, 0, len(serverIDs))
	for _, sid := range serverIDs {
		nodeID, nodeName, msg := h.ensureMtprotoNode(r.Context(), sid)
		if msg != "" {
			out = append(out, map[string]any{"server_id": sid, "ok": false, "error": msg})
			continue
		}
		rows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", nodeID)
		if err != nil || len(rows) == 0 {
			out = append(out, map[string]any{"server_id": sid, "ok": false, "error": "节点不存在"})
			continue
		}
		tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(rows[0], "template_id", 0))
		if err != nil {
			out = append(out, map[string]any{"server_id": sid, "ok": false, "error": "协议模板不存在"})
			continue
		}
		eff := mtprotoEffective(tpl, mtprotoNodeFromRow(rows[0]))
		created, restored := 0, 0
		for _, uuid := range uuids {
			var cid int64
			var status string
			err := h.db.QueryRowContext(r.Context(),
				`SELECT id, COALESCE(status,'active') FROM mtproto_clients WHERE node_id = ? AND uuid = ? LIMIT 1`,
				nodeID, uuid).Scan(&cid, &status)
			if err == nil {
				if status == "revoked" {
					_, _ = h.db.ExecContext(r.Context(),
						`UPDATE mtproto_clients SET status = 'active', revoked_at = 0, updated_at = ? WHERE id = ?`,
						nowMillis(), cid)
					restored++
				}
				continue
			}
			var email, owner string
			_ = h.db.QueryRowContext(r.Context(),
				`SELECT COALESCE(email,''), COALESCE(owner,'') FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).
				Scan(&email, &owner)
			secret := mtprotoGenerateSecret(eff.Domain)
			if secret == "" {
				continue
			}
			label := truncateRunes(firstNonEmpty(owner, email), 120)
			_, _ = h.db.ExecContext(r.Context(), `INSERT INTO mtproto_clients
			  (node_id, uuid, email, label, secret, status, remark, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, 'active', 'UUID 授权推送', ?, ?)`,
				nodeID, uuid, email, label, secret, nowMillis(), nowMillis())
			created++
		}
		changed, queued, msg := h.applyMtprotoToNode(r.Context(), nodeID)
		if msg != "" {
			out = append(out, map[string]any{"server_id": sid, "name": nodeName, "ok": false, "error": msg})
			continue
		}
		if !doPush {
			queued = false
		}
		out = append(out, map[string]any{
			"server_id": sid, "name": nodeName, "ok": true,
			"uuid_count": len(uuids), "created": created, "restored": restored,
			"changed": changed, "queued": queued,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": okCount > 0, "data": out, "ok_count": okCount, "total": len(out)})
}
