package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strings"
)

func (h *handlers) applyMtprotoToNode(ctx context.Context, nodeID int64) (bool, bool, string) {
	rows, err := h.mtprotoNodesWithMeta(ctx, "WHERE n.id = ?", nodeID)
	if err != nil || len(rows) == 0 {
		return false, false, "节点不存在"
	}
	row := rows[0]
	tpl, _, err := h.loadMtprotoTemplate(ctx, mapInt(row, "template_id", 0))
	if err != nil {
		return false, false, "协议模板不存在"
	}
	node := mtprotoNodeFromRow(row)
	eff := mtprotoEffective(tpl, node)
	var clients []mtprotoClient
	if cr, err := queryRowsAsMaps(ctx, h.db, `SELECT * FROM mtproto_clients WHERE node_id = ? ORDER BY id ASC`, nodeID); err == nil {
		for _, c := range cr {
			clients = append(clients, mtprotoClient{
				ID: mapInt(c, "id", 0), UUID: mapStr(c, "uuid", ""), Email: mapStr(c, "email", ""),
				Label: mapStr(c, "label", ""), Secret: mapStr(c, "secret", ""),
				Status: firstNonEmpty(mapStr(c, "status", ""), "active"),
			})
		}
	}
	content, _, _, msg := mtprotoRenderConfig(&eff, clients)
	if msg != "" {
		return false, false, msg
	}
	hash := sha256Hex(content)
	cfgName := "mtpx:" + node.Name + ":mtg"
	var cfgID int64
	var existHash, existPath, existRestart string
	err = h.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(content_hash,''), COALESCE(target_path,''), COALESCE(restart_cmd,'')
		   FROM node_configs WHERE name = ? LIMIT 1`, cfgName).
		Scan(&cfgID, &existHash, &existPath, &existRestart)
	changed := false
	now := nowMillis()
	serverID := mapInt(row, "server_id", 0)
	if err == sql.ErrNoRows {
		res, err := h.db.ExecContext(ctx, `INSERT INTO node_configs
		  (name, server_id, engine, target_path, service_name, restart_cmd, check_cmd, content, backup,
		   enabled, auto_push, push_interval_min, template_id, uuid, version, content_hash, created_at, updated_at)
		  VALUES (?, ?, 'mtproto', ?, ?, ?, ?, ?, 1, 1, 0, 0, 0, '', 1, ?, ?, ?)`,
			cfgName, serverID, eff.ConfigPath, eff.ServiceName, mtprotoReloadCmd, mtprotoCheckCmd, content, hash, now, now)
		if err != nil {
			return false, false, "写入配置失败"
		}
		cfgID, _ = res.LastInsertId()
		changed = true
	} else if err == nil {
		// existHash 与 hash 都是「content 原文」的 sha256（content_hash 口径）。
		if !(existHash == hash && existPath == eff.ConfigPath && existRestart == mtprotoReloadCmd) {
			_, _ = h.db.ExecContext(ctx, `UPDATE node_configs SET server_id = ?, engine = 'mtproto',
			  target_path = ?, service_name = ?, restart_cmd = ?, check_cmd = ?, content = ?, content_hash = ?,
			  version = COALESCE(version,0) + 1, enabled = 1, updated_at = ? WHERE id = ?`,
				serverID, eff.ConfigPath, eff.ServiceName, mtprotoReloadCmd, mtprotoCheckCmd, content, hash, now, cfgID)
			changed = true
		}
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE mtproto_nodes SET last_apply_at = ?, last_render_hash = ?, updated_at = ? WHERE id = ?`,
		now, hash, now, nodeID)
	queued := false
	if cfgID > 0 {
		if srv, err := h.fetchAgentServer(ctx, serverID); err == nil {
			if cfg, err := h.loadNodeConfig(ctx, cfgID); err == nil {
				payload := h.agentBuildDeployPayload(ctx, cfg, srv)
				_, _, qmsg := h.agentQueueTask(ctx, srv, "deploy", cfg.ID, payload, "mtproto")
				queued = qmsg == ""
			}
		}
	}
	return changed, queued, ""
}

// POST /api/mtproto/apply-all
func (h *handlers) mtprotoApplyAll(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	doPush := true
	if body != nil {
		if v, ok := body["push"].(bool); ok {
			doPush = v
		}
	}
	// 多节点且需要排队下发时转后台任务（避免请求线程里逐台 apply）
	if doPush {
		var total int64
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM mtproto_nodes WHERE enabled = 1`).Scan(&total)
		if total > 1 {
			jobID, n, dup, msg := h.batchCreateJob(r.Context(), "mtproto.apply_all", map[string]any{"push": true}, "admin")
			if msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
			if !dup {
				h.startBatchJob(jobID, "admin")
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true, "async": true, "job_id": jobID, "total": n, "duplicate": dup,
				"message": "已创建后台任务，进度见右上角「任务」",
			})
			return
		}
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT id, name FROM mtproto_nodes WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		changed, queued, msg := h.applyMtprotoToNode(r.Context(), id)
		if msg != "" {
			out = append(out, map[string]any{"id": id, "name": mapStr(row, "name", ""), "ok": false, "error": msg})
			continue
		}
		if !doPush {
			queued = false
		}
		out = append(out, map[string]any{
			"id": id, "name": mapStr(row, "name", ""), "ok": true,
			"changed": changed, "queued": queued,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": out, "ok_count": okCount, "total": len(out)})
}

// POST /api/mtproto/clients/{id}/send-mail
func (h *handlers) mtprotoClientSendMail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM mtproto_clients WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "客户端不存在")
		return
	}
	clientRow := rows[0]
	to := strings.TrimSpace(mapStr(body, "email", ""))
	if to == "" {
		to = strings.TrimSpace(mapStr(clientRow, "email", ""))
	}
	to = normalizeEmail(to)
	if to == "" {
		writeError(w, http.StatusBadRequest, "该客户端没有可用的邮箱")
		return
	}
	nodeRows, err := h.mtprotoNodesWithMeta(r.Context(), "WHERE n.id = ?", mapInt(clientRow, "node_id", 0))
	if err != nil || len(nodeRows) == 0 {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	tpl, _, err := h.loadMtprotoTemplate(r.Context(), mapInt(nodeRows[0], "template_id", 0))
	if err != nil {
		writeError(w, http.StatusNotFound, "协议模板不存在")
		return
	}
	eff := mtprotoEffective(tpl, mtprotoNodeFromRow(nodeRows[0]))
	client := &mtprotoClient{ID: id, UUID: mapStr(clientRow, "uuid", ""), Secret: mapStr(clientRow, "secret", "")}
	srv := mtprotoServer{
		LastIP4: mapStr(nodeRows[0], "server_ip4", ""),
		LastIP:  mapStr(nodeRows[0], "server_host", ""),
		Host:    mapStr(nodeRows[0], "server_host", ""),
	}
	link := mtprotoClientLink(&eff, srv, client)
	tg := mapStr(link, "tg", "")
	if tg == "" {
		writeError(w, http.StatusBadRequest, "生成链接失败：节点缺少有效 secret")
		return
	}
	html := fmt.Sprintf(`<div style="font-family:system-ui,sans-serif;max-width:560px;margin:0 auto;padding:24px">
<h2 style="margin:0 0 12px">Telegram 代理已就绪</h2>
<p>点击下面的链接即可在 Telegram 中添加代理（一台设备添加一次）：</p>
<p><a href="%s" style="display:inline-block;padding:12px 20px;background:#1677ff;color:#fff;border-radius:8px;text-decoration:none">一键添加代理</a></p>
<p style="color:#666;font-size:13px;word-break:break-all">%s</p>
<p style="color:#999;font-size:12px">如果按钮无法点击，请复制上面的链接到 Telegram 打开。此链接等同于密码，请勿公开分享。</p>
</div>`, tg, tg)
	if err := h.sendMail(to, "你的 Telegram 代理链接", html); err != nil {
		writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
		return
	}
	now := nowMillis()
	_, _ = h.db.ExecContext(r.Context(), `UPDATE mtproto_clients SET email = ?, delivered_at = ?, deliver_count = COALESCE(deliver_count,0) + 1,
	      last_mail_at = ?, updated_at = ? WHERE id = ?`, to, now, now, now, id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "email": to, "link": tg})
}
