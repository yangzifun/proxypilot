package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// SSH 安全防护：开启后仅允许内网网段访问 SSH。
//
// 放行清单（IPv4）：
//   - 100.96.0.0/12  Cloudflare WARP / 组网管理网
//   - 10.99.0.0/16   自建组网网段
// IPv6 不再放行任何来源网段（只保留 ESTABLISHED,RELATED），需要时再扩。
const (
	sshGuardCmd  = "PP_SSH_GUARD"
	sshGuardV4s  = "100.96.0.0/12 10.99.0.0/16"
	sshGuardV6s  = ""
)

// sshGuardEnableScript 开启防护并把规则持久化（重启后由 systemd 单元重新下发）。
func sshGuardEnableScript() string {
	return `set -u
V4S="` + sshGuardV4s + `"
V6S="` + sshGuardV6s + `"
CHAIN="` + sshGuardCmd + `"
PORTS=$(ss -tlnp 2>/dev/null | grep -E 'sshd|ssh' | awk '{print $4}' | sed 's/.*://' | sort -u | tr '\n' ' ')
[ -z "$PORTS" ] && PORTS=22

cat > /usr/local/sbin/pp-ssh-guard.sh <<'GUARD'
#!/bin/sh
set -u
V4S="` + sshGuardV4s + `"
V6S="` + sshGuardV6s + `"
CHAIN="` + sshGuardCmd + `"
PORTS=$(ss -tlnp 2>/dev/null | grep -E 'sshd|ssh' | awk '{print $4}' | sed 's/.*://' | sort -u | tr '\n' ' ')
[ -z "$PORTS" ] && PORTS=22
for BIN in iptables ip6tables; do
  command -v $BIN >/dev/null 2>&1 || continue
  CIDRS=$V4S
  [ "$BIN" = "ip6tables" ] && CIDRS=$V6S
  $BIN -N $CHAIN 2>/dev/null
  $BIN -F $CHAIN 2>/dev/null
  for CIDR in $CIDRS; do
    $BIN -A $CHAIN -s $CIDR -j ACCEPT
  done
  $BIN -A $CHAIN -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
  $BIN -A $CHAIN -j DROP
  for P in $PORTS; do
    $BIN -C INPUT -p tcp --dport $P -j $CHAIN 2>/dev/null || $BIN -I INPUT -p tcp --dport $P -j $CHAIN
  done
done
GUARD
chmod 700 /usr/local/sbin/pp-ssh-guard.sh

cat > /etc/systemd/system/pp-ssh-guard.service <<'UNIT'
[Unit]
Description=ProxyPilot SSH 安全防护（仅允许组网网段访问 SSH）
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/pp-ssh-guard.sh
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
cat > /etc/systemd/system/pp-ssh-guard.timer <<'TIMER'
[Unit]
Description=ProxyPilot SSH 防护定时校验（自愈：规则被清空时重新下发）

[Timer]
OnBootSec=90s
OnUnitActiveSec=30min
Unit=pp-ssh-guard.service

[Install]
WantedBy=timers.target
TIMER
systemctl daemon-reload
systemctl enable pp-ssh-guard.service >/dev/null 2>&1
systemctl enable --now pp-ssh-guard.timer >/dev/null 2>&1
systemctl restart pp-ssh-guard.service
echo "GUARD_ON ports=$PORTS"`}

// sshGuardDisableScript 关闭防护：移除挂载、清空链并停用开机单元。
func sshGuardDisableScript() string {
	return `set -u
CHAIN=` + sshGuardCmd + `
PORTS=$(ss -tlnp 2>/dev/null | grep -E 'sshd|ssh' | awk '{print $4}' | sed 's/.*://' | sort -u | tr '\n' ' ')
[ -z "$PORTS" ] && PORTS=22
systemctl disable --now pp-ssh-guard.timer >/dev/null 2>&1
systemctl disable --now pp-ssh-guard.service >/dev/null 2>&1
rm -f /etc/systemd/system/pp-ssh-guard.service /etc/systemd/system/pp-ssh-guard.timer /usr/local/sbin/pp-ssh-guard.sh
systemctl daemon-reload 2>/dev/null
for BIN in iptables ip6tables; do
  command -v $BIN >/dev/null 2>&1 || continue
  # 先移除所有指向该链的 INPUT 跳转（含历史遗留端口），否则 -X 无法删除链
  i=0
  while [ $i -lt 40 ] && $BIN -S INPUT 2>/dev/null | grep -q -- "-j $CHAIN"; do
    RULE=$($BIN -S INPUT 2>/dev/null | grep -- "-j $CHAIN" | head -1 | sed 's/^-A //')
    [ -z "$RULE" ] && break
    $BIN -D $RULE 2>/dev/null || break
    i=$((i+1))
  done
  for P in $PORTS 22 2222 22222; do
    i=0
    while [ $i -lt 8 ] && $BIN -C INPUT -p tcp --dport $P -j $CHAIN 2>/dev/null; do
      $BIN -D INPUT -p tcp --dport $P -j $CHAIN; i=$((i+1))
    done
  done
  $BIN -F $CHAIN 2>/dev/null
  $BIN -X $CHAIN 2>/dev/null
done
echo "GUARD_OFF"`
}

// POST /api/servers/{id}/ssh-guard  { "enabled": true|false }
func (h *handlers) serverSSHGuard(w http.ResponseWriter, r *http.Request) {
	id := jsonInt(pathParam(r, "id"))
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "缺少服务器 ID")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	enabled, _ := body["enabled"].(bool)

	srv, err := h.fetchAgentServer(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if !strings.EqualFold(srv.LinkMode, "agent") && !strings.EqualFold(srv.LinkMode, "local") {
		writeError(w, http.StatusBadRequest, "仅支持 Agent / 本地接入方式")
		return
	}

	script := sshGuardDisableScript()
	verb := "关闭"
	if enabled {
		script = sshGuardEnableScript()
		verb = "开启"
	}
	payload := map[string]any{"command": script, "timeout": 60}
	taskID, dup, msg := h.agentQueueTask(r.Context(), srv, "exec", 0, payload, "admin")
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	// 记录期望状态，界面据此展示开关
	state := int64(0)
	if enabled {
		state = 1
	}
	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE servers SET ssh_guard = ?, updated_at = ? WHERE id = ?`, state, nowMillis(), id)

	suffix := ""
	if dup {
		suffix = "，重复任务"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"task_id": taskID,
		"enabled": enabled,
		"message": fmt.Sprintf("已下发%s SSH 安全防护（任务 #%d%s）", verb, taskID, suffix),
	})
}

// GET /api/servers/{id}/ssh-guard 读取当前期望状态。
func (h *handlers) serverSSHGuardStatus(w http.ResponseWriter, r *http.Request) {
	id := jsonInt(pathParam(r, "id"))
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "缺少服务器 ID")
		return
	}
	var guard int64
	err := h.db.QueryRowContext(r.Context(), `SELECT COALESCE(ssh_guard,0) FROM servers WHERE id = ?`, id).Scan(&guard)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "enabled": guard == 1})
}

var _ = context.Background
