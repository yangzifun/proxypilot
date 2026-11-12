package api

import (
	"context"
	"database/sql"
	"net/http"
	"regexp"
	"strings"
)

// Agent 临时放行（agent_auth_grace）与令牌自愈。
//
// 事故背景（2026-11-12）：批量轮换接入令牌时，4 台节点的 /etc/proxypilot-agent.conf
// 被写入重复的令牌行（PROXYPILOT_TOKEN 与 TOKEN 各一行）；Agent 的配置解析按「后出现
// 的覆盖前面的」，实际生效的是旧令牌。旧令牌明文已不可复原（历史 agent_key_secret 变更
// 导致 enc:v1 解不开），面板按新令牌哈希查不到 → 401 → 节点掉线；但机器和 mesh 心跳都还在。
//
// 方案（运维一次性开关，用完自动撤销）：
//  1. 迁移 0053 给 servers 增加 agent_auth_grace（默认 0）；
//  2. 运维把故障节点的 agent_auth_grace 置 1；
//  3. agentAuthenticate 令牌哈希匹配失败时，若来源 IP 命中该服务器的
//     last_ip4 / last_ip / last_mesh_ip4，且 grace=1 → 放行并打 warn 日志；
//  4. 放行后的 poll 会给该节点排一条 exec 任务：把 conf 里所有令牌行统一改成当前
//     正确令牌（解密 agent_token_enc），然后重启 Agent；同一节点在一段时间窗口内只排一次；
//  5. 该节点用正常令牌认证成功后，代码自动把 agent_auth_grace 置 0（临时通道关闭）。
//
// 安全边界：
//   - 只对显式置 grace=1 的服务器生效，默认关闭；
//   - 仍要求令牌字符串带 ag_ 前缀且只含安全字符（不放行任意请求体）；
//   - 只信任真实客户端 IP（clientIP：仅可信反代才采信转发头），日志记录 server_id/remote/token_hint；
//   - 窗口内只排一次自愈任务，避免反复重启 Agent；operater 可随时把 grace 置 0 快速关闭。

const (
	// systemActionAgentTokenFix 是「重写 Agent conf 令牌」这类系统级固定动作的标记，
	// 与 cloudflared_update / warp_update 一样不受 allow_exec 限制（脚本由面板生成）。
	systemActionAgentTokenFix = "agent_token_fix"

	// agentTokenFixConfPath 与 Agent 安装脚本（install.sh）保持一致。
	agentTokenFixConfPath = "/etc/proxypilot-agent.conf"
	// agentTokenFixRetryWindowMS 同一节点在该窗口内最多排一次自愈任务。
	// 正常情况任务几秒内完成、节点重启后用新令牌认证成功即撤销 grace；
	// 若脚本失败（例如 conf 不存在），窗口过后才允许再试，避免反复重启。
	agentTokenFixRetryWindowMS = 6 * 3600 * 1000
)

// agentTokenShellSafe 只允许 Agent 令牌字符集（ag_ + hex）。令牌会被嵌入 shell 单引号，
// 必须严格校验，防止库里的异常值变成命令注入。
var agentTokenShellSafe = regexp.MustCompile(`^ag_[A-Za-z0-9]+$`)

// agentGraceAuthenticate 令牌哈希匹配失败后的兜底：按来源 IP 命中 grace 服务器则放行。
//
// 返回的 agentServer 上 GraceAuth=true，供 poll 阶段触发令牌自愈。
func (h *handlers) agentGraceAuthenticate(r *http.Request, raw string) (*agentServer, bool) {
	ip := strings.TrimSpace(clientIP(r))
	if ip == "" {
		return nil, false
	}
	s, err := scanAgentServerRow(h.db.QueryRowContext(r.Context(),
		agentServerSelectColumns+` WHERE agent_auth_grace = 1
		  AND ((last_ip4 <> '' AND last_ip4 = ?) OR (last_ip <> '' AND last_ip = ?) OR (last_mesh_ip4 <> '' AND last_mesh_ip4 = ?))
		  LIMIT 1`, ip, ip, ip))
	if err != nil {
		if err != sql.ErrNoRows {
			h.log.Error("Agent 临时放行查询失败", "err", err, "remote_ip", ip)
		}
		return nil, false
	}
	// 与正常路径保持同样的准入判断：只放行 agent 接入模式且启用的服务器。
	if s.LinkMode != "agent" || s.Enabled != 1 {
		return nil, false
	}
	s.GraceAuth = true
	h.log.Warn("Agent 令牌不匹配但命中临时放行（agent_auth_grace=1）",
		"server_id", s.ID, "server", s.Name, "remote_ip", ip,
		"token_hint", hintOf(raw), "path", r.URL.Path)
	return s, true
}

// clearAgentAuthGrace 正常令牌认证成功后撤销临时放行（grace → 0）。
//
// 单独查/写 agent_auth_grace，不写进 agentServerSelectColumns 主查询：迁移 0053
// 尚未应用时这里只会查询失败（直接跳过），不会影响正常 Agent 认证。
func (h *handlers) clearAgentAuthGrace(ctx context.Context, s *agentServer) {
	if s == nil {
		return
	}
	var grace int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(agent_auth_grace,0) FROM servers WHERE id = ?`, s.ID).Scan(&grace); err != nil {
		// 迁移未应用 / 数据库抖动：grace 是可选增强，不能让正常认证失败。
		return
	}
	if grace != 1 {
		return
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE servers SET agent_auth_grace = 0, updated_at = ? WHERE id = ? AND agent_auth_grace = 1`,
		nowMillis(), s.ID); err != nil {
		h.log.Warn("撤销 Agent 临时放行失败", "server_id", s.ID, "err", err)
		return
	}
	h.log.Warn("Agent 已用正常令牌认证成功，临时放行（agent_auth_grace）已自动撤销",
		"server_id", s.ID, "server", s.Name)
}

// ensureAgentTokenFixTask 给 grace 放行的节点排一条「重写 conf 令牌 + 重启 Agent」任务。
//
// 去重：同一 server 在 agentTokenFixRetryWindowMS 内只排一次（查 agent_tasks 里
// system_action=agent_token_fix 的历史任务，不依赖内存状态，面板重启后仍有效）。
func (h *handlers) ensureAgentTokenFixTask(ctx context.Context, srv *agentServer) {
	if srv == nil || !srv.GraceAuth {
		return
	}
	token := strings.TrimSpace(h.decodeAtRestValue(srv.AgentTokenEnc))
	if !agentTokenShellSafe.MatchString(token) {
		h.log.Warn("Agent 令牌自愈跳过：无法从 agent_token_enc 解出可用的新令牌",
			"server_id", srv.ID, "server", srv.Name, "has_enc", strings.TrimSpace(srv.AgentTokenEnc) != "")
		return
	}
	var exists int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE server_id = ? AND type = 'exec'
		   AND payload LIKE '%agent_token_fix%' AND created_at > ?`,
		srv.ID, nowMillis()-agentTokenFixRetryWindowMS).Scan(&exists); err != nil {
		h.log.Warn("Agent 令牌自愈去重查询失败", "server_id", srv.ID, "err", err)
		return
	}
	if exists > 0 {
		return
	}
	taskID, dup, msg := h.agentQueueTask(ctx, srv, "exec", 0, map[string]any{
		"command":       agentTokenFixCommand(token),
		"timeout":       120,
		"system_action": systemActionAgentTokenFix,
	}, "system")
	if msg != "" {
		h.log.Warn("Agent 令牌自愈任务入队失败", "server_id", srv.ID, "err", msg)
		return
	}
	h.log.Warn("已给临时放行节点排队令牌自愈任务（重写 conf 令牌并重启 Agent）",
		"server_id", srv.ID, "server", srv.Name, "task_id", taskID, "duplicate", dup)
}

// agentTokenFixCommand 生成自愈脚本。
//
// 设计要点：
//  1. 删除 conf 里所有令牌行（PROXYPILOT_TOKEN / TOKEN / KEY —— Agent 三者都认，
//     「后出现的覆盖前面的」，只改其中一种仍可能被后面的旧行覆盖），再追加唯一正确行；
//  2. 幂等：若 conf 里已只剩一条 PROXYPILOT_TOKEN 且值正确，直接 token-unchanged 退出、
//     不重启（任务回执丢失后被 cron 回收重投时不会造成重启循环）；
//  3. 先备份再原子替换，权限 600（与安装脚本一致）；脚本不输出令牌明文。
func agentTokenFixCommand(token string) string {
	return `set -u
CONF=` + agentTokenFixConfPath + `
TOKEN='` + token + `'
if [ ! -f "$CONF" ]; then echo "conf-missing"; exit 0; fi
tok_lines=$(grep -cE '^[[:space:]]*(PROXYPILOT_TOKEN|TOKEN|KEY)[[:space:]]*=' "$CONF" 2>/dev/null || true)
if [ "$tok_lines" = "1" ] && grep -qE "^PROXYPILOT_TOKEN=\"?$TOKEN\"?[[:space:]]*$" "$CONF" 2>/dev/null; then
  echo "token-unchanged"
  exit 0
fi
tmp=$(mktemp /tmp/pp-agent-conf.XXXXXX) || exit 1
grep -vE '^[[:space:]]*(PROXYPILOT_TOKEN|TOKEN|KEY)[[:space:]]*=' "$CONF" > "$tmp" 2>/dev/null || true
printf 'PROXYPILOT_TOKEN="%s"\n' "$TOKEN" >> "$tmp"
cp -a "$CONF" "$CONF.bak.$(date +%s)" 2>/dev/null || true
chmod 600 "$tmp"
mv "$tmp" "$CONF"
echo "token-rewritten"
systemctl --no-block restart proxypilot-agent 2>/dev/null || systemctl restart proxypilot-agent 2>/dev/null || true`
}
