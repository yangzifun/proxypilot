package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

func sha256Sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

func singboxDeriveSSPassword(serverKey, uuid string) string {
	sum := sha256Sum([]byte(serverKey + ":" + uuid))
	return base64.StdEncoding.EncodeToString(sum)
}

func singboxUsersFromUUIDs(tpl *singboxTemplate, uuids []string) []sbUser {
	clean := make([]string, 0, len(uuids))
	for _, uuid := range uuids {
		if s := strings.TrimSpace(uuid); s != "" {
			clean = append(clean, s)
		}
		if len(clean) >= 200 {
			break
		}
	}
	out := make([]sbUser, 0, len(clean))
	for i, uuid := range clean {
		out = append(out, sbUser{
			Name: fmt.Sprintf("user%02d", i+1), UUID: uuid,
			SSPassword: singboxDeriveSSPassword(tpl.SSServerKey, uuid),
		})
	}
	return out
}

// applySingboxNode 渲染并落库单节点；usersOverride 非 nil 时覆盖模板用户。
// 未显式覆盖时优先用节点授权的 uuid_list（与 UUID 巡检口径一致），避免把模板占位用户下发到线上。
func (h *handlers) applySingboxNode(ctx context.Context, nodeID int64, usersOverride []sbUser, force bool) ([]map[string]any, map[string]int, []map[string]string, int, string) {
	tpl, node, server, nodeRow, msg := h.loadSingboxNodeFull(ctx, nodeID)
	if msg != "" {
		return nil, nil, nil, 0, msg
	}
	if usersOverride == nil {
		usersOverride = singboxAuthorizedUsers(tpl, nodeRow)
	}
	if usersOverride != nil {
		b, _ := json.Marshal(usersOverride)
		tpl.UsersJSON = string(b)
	}
	files, ports, users, msg := singboxRenderBundle(tpl, node, server, nil)
	if msg != "" {
		return nil, nil, nil, 0, msg
	}
	// 下发节点同时刷新该节点的用户订阅配置（含 CDN 链接与 -cdn 备注），保证「设定后即出现在用户配置里」
	syncUsers := singboxAuthorizedUsers(tpl, nodeRow)
	if syncUsers == nil {
		_ = json.Unmarshal([]byte(tpl.UsersJSON), &syncUsers)
	}
	if len(syncUsers) > 0 {
		_, _, _ = h.syncSingboxConfigsForUsers(ctx, nodeID, node.Name, tpl, node, syncUsers)
	}

	now := nowMillis()
	out := make([]map[string]any, 0, len(files))
	changed := 0
	hashParts := make([]string, 0, len(files))

	for _, f := range files {
		key := mapStr(f, "key", "")
		cfgName := "sbx:" + node.Name + ":" + key
		content := mapStr(f, "content", "")
		path := mapStr(f, "path", "")
		service := mapStr(f, "service_name", "")
		checkCmd := mapStr(f, "check_cmd", "")
		engine := mapStr(f, "engine", "")
		hash := sha256Hex(content)
		restartCmd := singboxRestartCommand(service)
		hashParts = append(hashParts, key+"\x00"+content)

		var (
			existID      int64
			existHash    string
			existPath    string
			existRestart string
			existVersion int64
		)
		err := h.db.QueryRowContext(ctx, `SELECT id, COALESCE(content_hash,''), COALESCE(target_path,''),
		      COALESCE(restart_cmd,''), COALESCE(version,0) FROM node_configs WHERE name = ? LIMIT 1`, cfgName).
			Scan(&existID, &existHash, &existPath, &existRestart, &existVersion)
		if err == sql.ErrNoRows {
			res, err := h.db.ExecContext(ctx, `INSERT INTO node_configs
			  (name, server_id, engine, target_path, service_name, restart_cmd, check_cmd, content, backup,
			   enabled, auto_push, push_interval_min, template_id, uuid, install_versions, version, content_hash, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 0, 0, 0, '', ?, 1, ?, ?, ?)`,
				cfgName, mapInt(nodeRow, "server_id", 0), engine, path, service, restartCmd, checkCmd, content, hash, singboxInstallVersions(tpl), now, now)
			if err != nil {
				return nil, nil, nil, 0, "写入节点配置失败：" + err.Error()
			}
			newID, _ := res.LastInsertId()
			changed++
			out = append(out, map[string]any{
				"key": key, "name": cfgName, "engine": engine, "path": path,
				"service_name": service, "check_cmd": checkCmd, "content": content,
				"node_config_id": newID, "version": int64(1), "changed": true,
			})
			continue
		}
		if err != nil {
			return nil, nil, nil, 0, "查询节点配置失败"
		}
		same := existHash == hash && existPath == path && existRestart == restartCmd
		if same && !force {
			out = append(out, map[string]any{
				"key": key, "name": cfgName, "engine": engine, "path": path,
				"service_name": service, "check_cmd": checkCmd, "content": content,
				"node_config_id": existID, "version": existVersion, "changed": false,
			})
			continue
		}
		if _, err := h.db.ExecContext(ctx, `UPDATE node_configs SET server_id = ?, engine = ?, target_path = ?,
		      service_name = ?, restart_cmd = ?, check_cmd = ?, content = ?, content_hash = ?, install_versions = ?,
		      version = COALESCE(version,0) + 1, enabled = 1, updated_at = ? WHERE id = ?`,
			mapInt(nodeRow, "server_id", 0), engine, path, service, restartCmd, checkCmd, content, hash, singboxInstallVersions(tpl),
			now, existID); err != nil {
			return nil, nil, nil, 0, "下发失败"
		}
		changed++
		out = append(out, map[string]any{
			"key": key, "name": cfgName, "engine": engine, "path": path,
			"service_name": service, "check_cmd": checkCmd, "content": content,
			"node_config_id": existID, "version": existVersion + 1, "changed": true,
		})
	}

	renderHash := sha256Hex(strings.Join(hashParts, "\x00"))
	_, _ = h.db.ExecContext(ctx,
		`UPDATE singbox_nodes SET last_apply_at = ?, last_render_hash = ?, updated_at = ? WHERE id = ?`,
		now, renderHash, now, nodeID)
	return out, ports, users, changed, ""
}

// singboxAuthorizedUsers 读节点 uuid_list 生成用户列表；为空返回 nil（保持模板用户）。
func singboxAuthorizedUsers(tpl *singboxTemplate, nodeRow map[string]any) []sbUser {
	var list []string
	if raw := mapStr(nodeRow, "uuid_list", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &list)
	}
	clean := make([]string, 0, len(list))
	for _, u := range list {
		if s := strings.TrimSpace(u); s != "" {
			clean = append(clean, s)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	return singboxUsersFromUUIDs(tpl, clean)
}

// queueSingboxDeploy 把 node_configs 排成 Agent deploy 任务。
func (h *handlers) queueSingboxDeploy(ctx context.Context, files []map[string]any, actor string) []map[string]any {
	results := []map[string]any{}
	for _, f := range files {
		cfgID := mapInt(f, "node_config_id", 0)
		if cfgID == 0 {
			continue
		}
		cfg, err := h.loadNodeConfig(ctx, cfgID)
		if err != nil {
			results = append(results, map[string]any{"id": cfgID, "ok": false, "error": "配置不存在"})
			continue
		}
		srv, err := h.fetchAgentServer(ctx, cfg.ServerID)
		if err != nil {
			results = append(results, map[string]any{"id": cfgID, "ok": false, "error": "服务器不存在"})
			continue
		}
		payload := h.agentBuildDeployPayload(ctx, cfg, srv)
		taskID, dup, msg := h.agentQueueTask(ctx, srv, "deploy", cfg.ID, payload, actor)
		results = append(results, map[string]any{
			"id": cfg.ID, "name": cfg.Name, "task_id": taskID, "duplicate": dup, "ok": msg == "",
			"error": msg,
		})
	}
	return results
}

func (h *handlers) fetchAgentServer(ctx context.Context, id int64) (*agentServer, error) {
	var s agentServer
	err := h.db.QueryRowContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(link_mode,'ssh'), COALESCE(enabled,0),
	      COALESCE(agent_next_poll_sec,60), COALESCE(agent_version,''), COALESCE(agent_platform,''),
	      COALESCE(agent_token_enc,''), COALESCE(agent_last_poll_at,0), COALESCE(agent_poll_count,0),
	      COALESCE(agent_last_seen,0), COALESCE(last_status,'unknown'), COALESCE(metrics_every,1),
	      COALESCE(allow_exec,0), COALESCE(os_info,''), COALESCE(tags,''), COALESCE(username,''),
	      COALESCE(host,''), COALESCE(port,22), COALESCE(last_ip,''), COALESCE(last_ip4,''),
	      COALESCE(last_ip6,''), COALESCE(ip_country,''), COALESCE(ip_region,''), COALESCE(last_metrics,''),
	      COALESCE(agent_key_used_at,0)
	    FROM servers WHERE id = ? LIMIT 1`, id).
		Scan(&s.ID, &s.Name, &s.LinkMode, &s.Enabled, &s.NextPollSec, &s.AgentVersion, &s.AgentPlatform,
			&s.AgentTokenEnc, &s.AgentLastPollAt, &s.AgentPollCount, &s.AgentLastSeen, &s.LastStatus,
			&s.MetricsEvery, &s.AllowExec, &s.OSInfo, &s.Tags, &s.Username, &s.Host, &s.Port,
			&s.LastIP, &s.LastIP4, &s.LastIP6, &s.IPCountry, &s.IPRegion, &s.LastMetrics, &s.AgentKeyUsedAt)
	return &s, err
}

// POST /api/singbox/apply-all（同步；?sync=1 与默认行为一致）
func (h *handlers) singboxApplyAll(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	if body == nil {
		body = map[string]any{}
	}
	force, _ := body["force"].(bool)
	doPush := true
	if v, ok := body["push"].(bool); ok {
		doPush = v
	}
	// 多节点时转为后台任务，避免请求线程里逐台渲染/排队导致超时
	var total int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM singbox_nodes`).Scan(&total)
	if total > 1 {
		jobID, n, dup, msg := h.batchCreateJob(r.Context(), "singbox.apply_all",
			map[string]any{"force": force, "push": doPush}, "admin")
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
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT id, name FROM singbox_nodes ORDER BY id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		name := mapStr(row, "name", "")
		files, _, users, changed, msg := h.applySingboxNode(r.Context(), id, nil, force)
		if msg != "" {
			out = append(out, map[string]any{"id": id, "name": name, "ok": false, "error": msg})
			continue
		}
		queued := 0
		if doPush {
			q := h.queueSingboxDeploy(r.Context(), files, "apply-all")
			for _, item := range q {
				if ok, _ := item["ok"].(bool); ok {
					queued++
				}
			}
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "ok": true, "users": len(users),
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

// POST /api/singbox/deploy-all
func (h *handlers) singboxDeployAll(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	if body == nil {
		body = map[string]any{}
	}
	doPush := true
	if v, ok := body["push"].(bool); ok {
		doPush = v
	}
	// 多节点时转为后台任务（与 apply-all 共用任务类型，force=false）
	var total int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM singbox_nodes WHERE enabled = 1`).Scan(&total)
	if total > 1 {
		jobID, n, dup, msg := h.batchCreateJob(r.Context(), "singbox.apply_all",
			map[string]any{"force": false, "push": doPush}, "admin")
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
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT id, name FROM singbox_nodes WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		name := mapStr(row, "name", "")
		files, _, users, changed, msg := h.applySingboxNode(r.Context(), id, nil, false)
		if msg != "" {
			out = append(out, map[string]any{"id": id, "name": name, "ok": false, "error": msg})
			continue
		}
		queued := 0
		if doPush {
			for _, item := range h.queueSingboxDeploy(r.Context(), files, "deploy-all") {
				if ok, _ := item["ok"].(bool); ok {
					queued++
				}
			}
		}
		out = append(out, map[string]any{
			"id": id, "name": name, "ok": true, "users": len(users),
			"changed": changed, "queued": queued,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(out), "ok_count": okCount, "data": out})
}
