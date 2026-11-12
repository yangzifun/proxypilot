package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type serverInput struct {
	Name              *string `json:"name"`
	Host              *string `json:"host"`
	Tags              *string `json:"tags"`
	Remark            *string `json:"remark"`
	Enabled           *bool   `json:"enabled"`
	AllowExec         *bool   `json:"allow_exec"`
	MetricsEvery      *int64  `json:"metrics_every"`
	AgentNextPollSec  *int64  `json:"agent_next_poll_sec"`
	SingboxTemplateID *int64  `json:"singbox_template_id"`
	SingboxDomain     *string `json:"singbox_domain"`
	SingboxWarp       *int64  `json:"singbox_warp"`
	SingboxAutoDeploy *int64  `json:"singbox_auto_deploy"`
}

var hostRe = regexp.MustCompile(`^[a-zA-Z0-9._:\[\]-]*$`)

func (in *serverInput) normalize() error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return errors.New("服务器名称不能为空")
		}
		if len([]rune(name)) > 64 {
			name = string([]rune(name)[:64])
		}
		in.Name = &name
	}
	if in.Host != nil {
		addr := strings.TrimSpace(*in.Host)
		addr = regexp.MustCompile(`^[a-zA-Z]+://`).ReplaceAllString(addr, "")
		addr = regexp.MustCompile(`/.*$`).ReplaceAllString(addr, "")
		addr = regexp.MustCompile(`:\d+$`).ReplaceAllString(addr, "")
		if len(addr) > 255 {
			addr = addr[:255]
		}
		if !hostRe.MatchString(addr) {
			return errors.New("机器地址格式不正确（IP 或域名）")
		}
		in.Host = &addr
	}
	if in.Tags != nil {
		t := strings.TrimSpace(*in.Tags)
		in.Tags = &t
	}
	if in.Remark != nil {
		t := strings.TrimSpace(*in.Remark)
		in.Remark = &t
	}
	if in.AgentNextPollSec != nil {
		v := clampInt(*in.AgentNextPollSec, 2, 900)
		in.AgentNextPollSec = &v
	}
	if in.MetricsEvery != nil {
		v := clampInt(*in.MetricsEvery, 1, 10)
		in.MetricsEvery = &v
	}
	if in.SingboxWarp != nil {
		v := *in.SingboxWarp
		if v != -1 && v != 0 && v != 1 {
			v = -1
		}
		in.SingboxWarp = &v
	}
	return nil
}

func boolToInt(p *bool, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	if *p {
		return 1
	}
	return 0
}

// POST /api/servers
func (h *handlers) serverCreate(w http.ResponseWriter, r *http.Request) {
	var in serverInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if err := in.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Name == nil || *in.Name == "" {
		writeError(w, http.StatusBadRequest, "服务器名称不能为空")
		return
	}
	var dup int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT id FROM servers WHERE name = ? LIMIT 1`, *in.Name).Scan(&dup); err == nil {
		writeError(w, http.StatusConflict, "服务器名称已存在")
		return
	}

	token, err := generateAgentToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成接入密钥失败")
		return
	}
	enc, err := encryptAgentToken(h.agentKeySecret(), token)
	if err != nil {
		h.log.Warn("加密接入密钥失败（仍会返回明文令牌）", "err", err)
		enc = ""
	}
	host := ""
	if in.Host != nil {
		host = *in.Host
	}
	now := nowMillis()

	res, err := h.db.ExecContext(r.Context(), `INSERT INTO servers
	  (name, host, port, username, auth_type, secret_enc, secret_hint, tags, remark,
	   enabled, last_status, link_mode, agent_token_hash, agent_token_hint, agent_token_enc,
	   agent_next_poll_sec, metrics_every, allow_exec, skip_hostkey_verify,
	   singbox_template_id, singbox_domain, singbox_warp, singbox_auto_deploy,
	   agent_key_created_at, agent_key_used_at, created_at, updated_at)
	  VALUES (?, ?, 22, 'agent', 'password', NULL, '', ?, ?, ?, 'unknown', 'agent', ?, ?, ?,
	          ?, ?, ?, 0, ?, ?, ?, ?, ?, 0, ?, ?)`,
		*in.Name, host, strOr(in.Tags, ""), strOr(in.Remark, ""),
		boolToInt(in.Enabled, 1), sha256Hex(token), agentTokenHint(token), enc,
		intOrValue(in.AgentNextPollSec, 2), intOrValue(in.MetricsEvery, 1), boolToInt(in.AllowExec, 0),
		intOrValue(in.SingboxTemplateID, 0), strOr(in.SingboxDomain, ""), intOrValue(in.SingboxWarp, -1),
		intOrValue(in.SingboxAutoDeploy, 0), now, now, now)
	if err != nil {
		h.log.Error("创建服务器失败", "err", err)
		writeError(w, http.StatusInternalServerError, "创建服务器失败")
		return
	}
	id, _ := res.LastInsertId()
	if id > 0 {
		go h.geoRefreshServer(context.Background(), id)
	}
	row, err := h.fetchServerRow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}
	origin := requestOrigin(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"data":            row,
		"agent_token":     token,
		"install_command": h.agentInstallCommand(r.Context(), origin, token),
		"message":         "请把 install_command 复制到服务器执行；密钥已加密保存，之后可在后台随时查看",
		"probe":           nil,
	})
}

func strOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}

func intOrValue(p *int64, fallback int64) int64 {
	if p == nil {
		return fallback
	}
	return *p
}

// serverID 解析路径里的 id，非数字返回 false（交给代理/404）。
func serverID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// GET /api/servers/{id}
func (h *handlers) serverGet(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		h.notMigrated(w, r)
		return
	}
	row, err := h.fetchServerRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if err != nil {
		h.log.Error("查询服务器失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询服务器失败")
		return
	}

	// 补自建组网地址（10.99.0.0/16），与 WARP 的 100.96.x 区分展示
	{
		list := []serverRow{row}
		h.attachMeshInfo(r.Context(), list)
		row = list[0]
	}

	metricsRows, err := h.db.QueryContext(r.Context(),
		`SELECT COALESCE(status,''), COALESCE(latency_ms,0), COALESCE(cpu_percent,0), COALESCE(mem_percent,0),
		        COALESCE(disk_percent,0), COALESCE(load1,0), COALESCE(created_at,0)
		   FROM server_metrics WHERE server_id = ? ORDER BY id DESC LIMIT 60`, id)
	var metrics []map[string]any
	if err == nil {
		for metricsRows.Next() {
			var status string
			var latency, createdAt int64
			var cpu, mem, disk, load float64
			if metricsRows.Scan(&status, &latency, &cpu, &mem, &disk, &load, &createdAt) == nil {
				metrics = append(metrics, map[string]any{
					"status": status, "latency_ms": latency, "cpu_percent": cpu,
					"mem_percent": mem, "disk_percent": disk, "load1": load, "created_at": createdAt,
				})
			}
		}
		metricsRows.Close()
	}
	for i, j := 0, len(metrics)-1; i < j; i, j = i+1, j-1 {
		metrics[i], metrics[j] = metrics[j], metrics[i]
	}

	cfgRows, err := h.db.QueryContext(r.Context(),
		`SELECT id, COALESCE(name,''), COALESCE(engine,''), COALESCE(target_path,''), COALESCE(service_name,''),
		        COALESCE(enabled,0), COALESCE(auto_push,0), COALESCE(last_push_at,0), COALESCE(last_push_status,'')
		   FROM node_configs WHERE server_id = ? ORDER BY id ASC`, id)
	var nodeConfigs []map[string]any
	if err == nil {
		for cfgRows.Next() {
			var (
				cid, enabled, autoPush, lastPush int64
				name, engine, target, service    string
				pushStatus                       string
			)
			if cfgRows.Scan(&cid, &name, &engine, &target, &service, &enabled, &autoPush, &lastPush, &pushStatus) == nil {
				nodeConfigs = append(nodeConfigs, map[string]any{
					"id": cid, "name": name, "engine": engine, "target_path": target,
					"service_name": service, "enabled": enabled == 1, "auto_push": autoPush == 1,
					"last_push_at": lastPush, "last_push_status": pushStatus,
				})
			}
		}
		cfgRows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":        true,
		"data":           row,
		"metrics_recent": metrics,
		"node_configs":   nodeConfigs,
	})
}

// PUT /api/servers/{id}
func (h *handlers) serverUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		h.notMigrated(w, r)
		return
	}
	cur, err := h.fetchServerRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询服务器失败")
		return
	}

	var in serverInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if err := in.normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Name != nil {
		var dup int64
		if err := h.db.QueryRowContext(r.Context(),
			`SELECT id FROM servers WHERE name = ? AND id <> ? LIMIT 1`, *in.Name, id).Scan(&dup); err == nil {
			writeError(w, http.StatusConflict, "服务器名称已存在")
			return
		}
	}

	sets := []string{}
	args := []any{}
	add := func(expr string, v any) { sets = append(sets, expr); args = append(args, v) }
	if in.Name != nil {
		add("name = ?", *in.Name)
	}
	if in.Host != nil {
		add("host = ?", *in.Host)
	}
	if in.Tags != nil {
		add("tags = ?", *in.Tags)
	}
	if in.Remark != nil {
		add("remark = ?", *in.Remark)
	}
	if in.Enabled != nil {
		add("enabled = ?", boolToInt(in.Enabled, 1))
	}
	if in.AllowExec != nil {
		add("allow_exec = ?", boolToInt(in.AllowExec, 0))
	}
	if in.MetricsEvery != nil {
		add("metrics_every = ?", *in.MetricsEvery)
	}
	if in.AgentNextPollSec != nil {
		add("agent_next_poll_sec = ?", *in.AgentNextPollSec)
	}
	if in.SingboxTemplateID != nil {
		add("singbox_template_id = ?", *in.SingboxTemplateID)
	}
	if in.SingboxDomain != nil {
		add("singbox_domain = ?", *in.SingboxDomain)
	}
	if in.SingboxWarp != nil {
		add("singbox_warp = ?", *in.SingboxWarp)
	}
	if in.SingboxAutoDeploy != nil {
		add("singbox_auto_deploy = ?", *in.SingboxAutoDeploy)
	}

	newToken := ""
	if !cur.HasAgentToken {
		// 历史记录（SSH 时代）补齐接入密钥
		token, err := generateAgentToken()
		if err == nil {
			newToken = token
			enc, _ := encryptAgentToken(h.agentKeySecret(), token)
			add("agent_token_hash = ?", sha256Hex(token))
			add("agent_token_hint = ?", agentTokenHint(token))
			add("agent_token_enc = ?", enc)
			add("agent_key_created_at = ?", nowMillis())
		}
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "没有需要更新的字段")
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowMillis(), id)
	if _, err := h.db.ExecContext(r.Context(),
		"UPDATE servers SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...); err != nil {
		h.log.Error("更新服务器失败", "err", err)
		writeError(w, http.StatusInternalServerError, "更新服务器失败")
		return
	}
	row, err := h.fetchServerRow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取服务器失败")
		return
	}
	resp := map[string]any{"success": true, "data": row}
	if newToken != "" {
		resp["agent_token"] = newToken
		resp["install_command"] = h.agentInstallCommand(r.Context(), requestOrigin(r), newToken)
		resp["message"] = "已切换为 Agent 接入；令牌明文仅显示这一次"
	}
	writeJSON(w, http.StatusOK, resp)
}

// serverDeleteCascadeSQL 删除服务器时必须按顺序执行的级联清理。
//
// 顺序规则：
//  1. 引用 singbox_nodes / mihomo_nodes / mtproto_nodes / vpn_nodes 的子表必须先删
//     （用 node_id JOIN 父表定位），父表删掉之后就再也定位不到这些孤儿行；
//  2. configs 里由 sing-box 节点生成的订阅配置也在这里清掉；
//  3. servers 必须最后删。
//
// 审计 P2-9：旧实现只清 11 张表，agent_tasks / 流量配额 / 解锁检查 / 终端会话 /
// 端口映射 / 组网成员 / VPN 证书与链路 / MTProto 客户端 / 节点流量统计等表都会留孤儿行。
var serverDeleteCascadeSQL = []string{
	// 订阅侧授权：configs.singbox_node_id 指向 sing-box 节点
	`DELETE c FROM configs c JOIN singbox_nodes n ON n.id = c.singbox_node_id WHERE n.server_id = ?`,
	// sing-box 节点统计（node_id 指向 singbox_nodes.id）
	`DELETE t FROM node_connections t JOIN singbox_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE t FROM node_stats_snapshot t JOIN singbox_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE t FROM node_user_counter t JOIN singbox_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE t FROM user_node_recent t JOIN singbox_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE t FROM user_traffic_hourly t JOIN singbox_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE FROM singbox_nodes WHERE server_id = ?`,
	// mihomo 节点统计
	`DELETE t FROM mihomo_traffic_hourly t JOIN mihomo_nodes n ON n.id = t.node_id WHERE n.server_id = ?`,
	`DELETE FROM mihomo_nodes WHERE server_id = ?`,
	// MTProto：先客户端再节点
	`DELETE c FROM mtproto_clients c JOIN mtproto_nodes n ON n.id = c.node_id WHERE n.server_id = ?`,
	`DELETE FROM mtproto_nodes WHERE server_id = ?`,
	// VPN：先授权 / 会话 / 链路 / 证书，再节点
	`DELETE g FROM vpn_node_grants g JOIN vpn_nodes n ON n.id = g.node_id WHERE n.server_id = ?`,
	`DELETE s FROM vpn_sessions s JOIN vpn_nodes n ON n.id = s.node_id WHERE n.server_id = ?`,
	`DELETE l FROM vpn_node_links l JOIN vpn_nodes n ON n.id = l.node_id WHERE n.server_id = ?`,
	`DELETE c FROM vpn_certs c JOIN vpn_nodes n ON n.id = c.node_id WHERE n.server_id = ?`,
	`DELETE FROM vpn_nodes WHERE server_id = ?`,
	// 直接带 server_id 的表
	`DELETE FROM node_configs WHERE server_id = ?`,
	`DELETE FROM server_metrics WHERE server_id = ?`,
	`DELETE FROM node_push_logs WHERE server_id = ?`,
	`DELETE FROM agent_tasks WHERE server_id = ?`,
	`DELETE FROM server_traffic_limits WHERE server_id = ?`,
	`DELETE FROM server_traffic_usage WHERE server_id = ?`,
	`DELETE FROM server_unlock_checks WHERE server_id = ?`,
	`DELETE FROM shell_sessions WHERE server_id = ?`,
	`DELETE FROM uuid_port_map WHERE server_id = ?`,
	`DELETE FROM vpn_mesh_nodes WHERE server_id = ?`,
	`DELETE FROM servers WHERE id = ?`,
}

// DELETE /api/servers/{id}
func (h *handlers) serverDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		h.notMigrated(w, r)
		return
	}
	if _, err := h.fetchServerRow(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	defer tx.Rollback()
	// 先清理这台服务器关联的一切（VPN 节点 / 授权 / 会话 / 任务 / 统计 / 配置）。
	//
	// 之前删服务器只清了 node_configs / metrics / push_logs，后续补了一轮 VPN 节点，
	// 仍有 13 类关联表会留孤儿行；清单与顺序见 serverDeleteCascadeSQL。
	// 必须排在删 servers 之前，否则 server_id 已经无主、无法定位。
	for _, q := range serverDeleteCascadeSQL {
		if _, err := tx.ExecContext(r.Context(), q, id); err != nil {
			h.log.Error("删除服务器失败", "err", err)
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "服务器及其节点配置 / VPN 节点与授权 / 监控数据已删除",
	})
}

// POST /api/servers/{id}/agent-token
func (h *handlers) serverRotateToken(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		h.notMigrated(w, r)
		return
	}
	if _, err := h.fetchServerRow(r.Context(), id); errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	token, err := generateAgentToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "生成接入密钥失败")
		return
	}
	enc, _ := encryptAgentToken(h.agentKeySecret(), token)
	now := nowMillis()
	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE servers SET agent_token_hash = ?, agent_token_hint = ?, agent_token_enc = ?,
		   agent_poll_count = 0, agent_key_created_at = ?, agent_key_used_at = 0, updated_at = ?
		 WHERE id = ?`,
		sha256Hex(token), agentTokenHint(token), enc, now, now, id); err != nil {
		h.log.Error("轮换接入密钥失败", "err", err)
		writeError(w, http.StatusInternalServerError, "轮换失败")
		return
	}
	cmd := h.agentInstallCommand(r.Context(), requestOrigin(r), token)
	if r.URL.Query().Get("disable_ssh") == "1" {
		cmd += " --disable-ssh"
	}
	row, _ := h.fetchServerRow(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"agent_token":     token,
		"install_command": cmd,
		"message":         "旧令牌已失效；请在服务器上重新执行安装命令（或更新 /etc/proxypilot-agent.conf 中的 PROXYPILOT_TOKEN 后重启 timer）",
		"data":            row,
	})
}
