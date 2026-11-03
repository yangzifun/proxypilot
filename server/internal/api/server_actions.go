package api

import (
	"net/http"
	"strings"
)

// /api/servers/{id}/{action}
func (h *handlers) serverAction(w http.ResponseWriter, r *http.Request) {
	id := jsonInt(pathParam(r, "id"))
	action := strings.ToLower(pathParam(r, "action"))
	if id <= 0 {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM servers WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	server := rows[0]

	switch action {
	case "agent-key":
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		if mapStr(server, "link_mode", "agent") != "agent" {
			writeError(w, http.StatusBadRequest, "该服务器不是 Agent 接入方式")
			return
		}
		hint := mapStr(server, "agent_token_hint", "")
		token := decryptAgentToken(h.agentKeySecret(), mapStr(server, "agent_token_enc", ""))
		if token == "" {
			writeJSON(w, http.StatusConflict, map[string]any{
				"success": false, "ok": false, "viewable": false, "reason": "not_recoverable",
				"token_hint": hint,
				"error":      "该密钥不可回溯（创建于加密启用前，或 PP_AGENT_KEY_SECRET 变更过）：请重置接入密钥",
			})
			return
		}
		h.recordNodeLog(r.Context(), 0, id, "agent-key-view", "success", 0, "查看接入密钥", "", "admin")
		disableSSH := r.URL.Query().Get("disable_ssh") == "1"
		cmd := h.agentInstallCommand(requestOrigin(r), token)
		if disableSSH {
			cmd += " --disable-ssh"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "ok": true, "viewable": true,
			"agent_token": token, "token_hint": hint, "disable_ssh": disableSSH,
			"install_command": cmd,
			"note":            "密钥为敏感凭据：请勿截图外发；本次查看已记入下发 / 执行日志（action=agent-key-view）。",
		})

	case "agent-install":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		token := decryptAgentToken(h.agentKeySecret(), mapStr(server, "agent_token_enc", ""))
		if token == "" {
			writeError(w, http.StatusBadRequest, "无法生成安装命令：密钥不可查看，请先重置接入密钥")
			return
		}
		cmd := h.agentInstallCommand(requestOrigin(r), token)
		if r.URL.Query().Get("disable_ssh") == "1" {
			cmd += " --disable-ssh"
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "install_command": cmd})

	case "tasks":
		tasks, err := queryRowsAsMaps(r.Context(), h.db, `SELECT t.*, c.name AS config_name
		    FROM agent_tasks t LEFT JOIN node_configs c ON c.id = t.config_id
		    WHERE t.server_id = ? ORDER BY t.id DESC LIMIT 100`, id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询任务失败")
			return
		}
		var pending int64
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM agent_tasks WHERE server_id = ? AND status IN ('pending','sent')`, id).Scan(&pending)
		data := make([]map[string]any, 0, len(tasks))
		for _, t := range tasks {
			data = append(data, agentTaskJSON(t))
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data, "pending": pending})

	case "test", "monitor":
		if mode := mapStr(server, "link_mode", "agent"); mode != "agent" && mode != "local" {
			writeError(w, http.StatusBadRequest, "仅支持 Agent / 本地接入方式")
			return
		}
		agent, err := h.fetchAgentServer(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		taskID, dup, msg := h.agentQueueTask(r.Context(), agent, "probe", 0, map[string]any{}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		pollSec := mapInt(server, "agent_next_poll_sec", 10)
		msg = "已排队探测任务，Agent 下次轮询（≤ " + itoa64(pollSec) + " 秒）后即可看到最新指标"
		if mapStr(server, "link_mode", "agent") == "local" {
			msg = "本地探测已排队，通常 1–2 秒内完成"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "mode": "agent", "queued": true, "task_id": taskID, "duplicate": dup,
			"message": msg, "server": server,
		})

	case "metrics":
		hours := intOrJS(r.URL.Query().Get("hours"), 24, 1, 24*30)
		limit := clampInt(int64(intOrJS(r.URL.Query().Get("limit"), 200, 1, 1000)), 1, 1000)
		samples, err := queryRowsAsMaps(r.Context(), h.db,
			`SELECT status, latency_ms, cpu_percent, mem_percent, disk_percent, load1, load5, load15,
			        uptime_sec, services, created_at, net_rx_rate, net_tx_rate, disk_read_rate, disk_write_rate,
			        conn_total, conn_established, conn_timewait, proc_total
			   FROM server_metrics WHERE server_id = ? AND created_at >= ? ORDER BY id DESC LIMIT ?`,
			id, nowMillis()-int64(hours)*3600_000, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询指标失败")
			return
		}
		for i, j := 0, len(samples)-1; i < j; i, j = i+1, j-1 {
			samples[i], samples[j] = samples[j], samples[i]
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "hours": hours, "total": len(samples), "data": samples})

	case "reboot":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		mode := mapStr(server, "link_mode", "agent")
		if mode != "agent" && mode != "local" {
			writeError(w, http.StatusBadRequest, "仅支持 Agent / 本地接入方式")
			return
		}
		agent, err := h.fetchAgentServer(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		// 延迟 2 秒再重启，让 Agent 有机会回执；reboot 失败时回退 shutdown。
		cmd := "nohup sh -c 'sleep 2; systemctl reboot || reboot || shutdown -r now' >/dev/null 2>&1 &"
		taskID, dup, msg := h.agentQueueTask(r.Context(), agent, "exec", 0,
			map[string]any{"command": cmd, "timeout": 15}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		h.recordNodeLog(r.Context(), 0, id, "server-reboot", "success", 0, "下发重启指令", "", "admin")
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "queued": true, "task_id": taskID, "duplicate": dup,
			"message": "重启指令已下发，服务器将在约 2 秒后重启",
		})

	case "update-cloudflared":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		mode := mapStr(server, "link_mode", "agent")
		if mode != "agent" && mode != "local" {
			writeError(w, http.StatusBadRequest, "仅支持 Agent / 本地接入方式")
			return
		}
		if _, err := h.fetchAgentServer(r.Context(), id); err != nil {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		// 进入后台任务：任务中心可见进度与 OLD/NEW 版本结果
		jobID, n, dup, msg := h.batchCreateJob(r.Context(), "cloudflared.update", map[string]any{"server_id": id}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		if !dup {
			h.startBatchJob(jobID, "admin")
		}
		h.recordNodeLog(r.Context(), 0, id, "update-cloudflared", "success", 0, "更新 cloudflared（后台任务）", "", "admin")
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "async": true, "job_id": jobID, "total": n, "duplicate": dup,
			"message": "已创建后台任务，进度见右上角「任务」",
		})
	case "exec":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
			return
		}
		if mapInt(server, "allow_exec", 0) != 1 {
			writeError(w, http.StatusForbidden, "该服务器未开启「允许执行命令」（allow_exec）")
			return
		}
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		command := strings.TrimSpace(mapStr(body, "command", ""))
		if command == "" {
			writeError(w, http.StatusBadRequest, "命令不能为空")
			return
		}
		if len([]rune(command)) > 4000 {
			writeError(w, http.StatusBadRequest, "命令过长（最长 4000 字符）")
			return
		}
		timeoutSec := intOrJS(body["timeout"], 30000, 2000, 60000) / 1000
		if timeoutSec < 2 {
			timeoutSec = 2
		}
		if timeoutSec > 60 {
			timeoutSec = 60
		}
		agent, err := h.fetchAgentServer(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		taskID, dup, msg := h.agentQueueTask(r.Context(), agent, "exec", 0,
			map[string]any{"command": command, "timeout": timeoutSec}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		msg = "命令已排队；Agent 下次轮询执行后可在任务队列查看输出"
		if mapStr(server, "link_mode", "agent") == "local" {
			msg = "命令已在本机执行，稍后可在任务队列查看输出"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "mode": "agent", "queued": true, "task_id": taskID, "duplicate": dup, "message": msg,
		})

	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

// DELETE /api/agent/tasks/{id}
func (h *handlers) agentTaskDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	var status string
	if err := h.db.QueryRowContext(r.Context(), `SELECT COALESCE(status,'') FROM agent_tasks WHERE id = ?`, id).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	if status == "done" || status == "failed" {
		writeError(w, http.StatusBadRequest, "任务已结束，无法取消")
		return
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE agent_tasks SET status = 'expired', finished_at = ?, output = ? WHERE id = ?`, nowMillis(), "已由管理员取消", id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "任务已取消"})
}

// cloudflaredUpdateCommand 更新 cloudflared 的固定脚本（后台任务复用）。
func cloudflaredUpdateCommand() string {
	return `set -e
arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported arch: $arch"; exit 1 ;; esac
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
curl -sS -L --fail --max-time 180 -o "$tmp" "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-$arch"
chmod 755 "$tmp"
unit_bin=$(systemctl show -p ExecStart --value cloudflared 2>/dev/null | sed -n 's/.*path=\([^ ;]*\).*/\1/p' | head -1)
[ -n "$unit_bin" ] || unit_bin=$(command -v cloudflared || echo /usr/local/bin/cloudflared)
old=$("$unit_bin" --version 2>&1 | head -1 || true)
new_ver=$("$tmp" --version 2>&1 | head -1 || true)
if [ "$old" = "$new_ver" ]; then
  echo "BIN=$unit_bin"
  echo "OLD=$old"
  echo "NEW=$new_ver"
  echo "ACTIVE=$(systemctl is-active cloudflared 2>/dev/null || true)"
  echo "ALREADY=$old"
  exit 0
fi
[ -f "$unit_bin" ] && cp -a "$unit_bin" "$unit_bin.bak.$(date +%s)" 2>/dev/null || true
install -m 755 "$tmp" "$unit_bin"
[ "$unit_bin" = "/usr/local/bin/cloudflared" ] || install -m 755 "$tmp" /usr/local/bin/cloudflared 2>/dev/null || true
systemctl restart cloudflared 2>/dev/null || systemctl start cloudflared 2>/dev/null || true
sleep 2
now=$("$unit_bin" --version 2>&1 | head -1 || true)
echo "BIN=$unit_bin"
echo "OLD=$old"
echo "NEW=$now"
echo "ACTIVE=$(systemctl is-active cloudflared 2>/dev/null || true)"
if [ "$old" = "$now" ]; then echo "更新未生效：版本未变化"; exit 1; fi`
}
