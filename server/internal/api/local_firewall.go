package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// 本机端口开放/关闭：通过独立 iptables 链 PP_PORTS 管理，
// 规则持久化到 pp-port-rules.service，重启后自动恢复。

const (
	portChain   = "PP_PORTS"
	portScript  = "/usr/local/sbin/pp-port-rules.sh"
	portUnit    = "/etc/systemd/system/pp-port-rules.service"
	portProtoOK = "tcp,udp"
)

type portRule struct {
	ID       int64  `json:"id"`
	Port     int64  `json:"port"`
	Protocol string `json:"protocol"`
	Action   string `json:"action"`
	Note     string `json:"note"`
}

func (h *handlers) localPortRules(r *http.Request) ([]portRule, error) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, port, COALESCE(protocol,'tcp'), COALESCE(action,'open'), COALESCE(note,'')
		   FROM local_port_rules ORDER BY port ASC, protocol ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []portRule{}
	for rows.Next() {
		var p portRule
		if rows.Scan(&p.ID, &p.Port, &p.Protocol, &p.Action, &p.Note) == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// applyPortRules 依据数据库重建 PP_PORTS 链，并写入开机恢复脚本。
func (h *handlers) applyPortRules(r *http.Request) error {
	rules, err := h.localPortRules(r)
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\nset -u\nCHAIN=" + portChain + "\n")
	sb.WriteString("for BIN in iptables ip6tables; do\n")
	sb.WriteString("  command -v $BIN >/dev/null 2>&1 || continue\n")
	sb.WriteString("  $BIN -N $CHAIN 2>/dev/null\n  $BIN -F $CHAIN 2>/dev/null\n")
	for _, p := range rules {
		proto := strings.ToLower(p.Protocol)
		if proto != "udp" {
			proto = "tcp"
		}
		target := "ACCEPT"
		if p.Action == "close" {
			target = "DROP"
		}
		sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s --dport %d -j %s\n", proto, p.Port, target))
	}
	sb.WriteString("  $BIN -C INPUT -j $CHAIN 2>/dev/null || $BIN -I INPUT 1 -j $CHAIN\n")
	sb.WriteString("done\n")

	script := sb.String()
	if out, err := exec.Command("/bin/sh", "-c",
		"cat > "+portScript+" <<'EOF'\n"+script+"EOF\nchmod 700 "+portScript).CombinedOutput(); err != nil {
		return fmt.Errorf("写入脚本失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	unit := `[Unit]
Description=ProxyPilot 端口规则（开机恢复）
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=` + portScript + `
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`
	if out, err := exec.Command("/bin/sh", "-c",
		"cat > "+portUnit+" <<'EOF'\n"+unit+"EOF\nsystemctl daemon-reload && systemctl enable pp-port-rules.service >/dev/null 2>&1; systemctl restart pp-port-rules.service").CombinedOutput(); err != nil {
		return fmt.Errorf("应用规则失败：%v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// GET /api/local/firewall
func (h *handlers) localFirewallList(w http.ResponseWriter, r *http.Request) {
	rules, err := h.localPortRules(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取端口规则失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rules})
}

// POST /api/local/firewall {port, protocol, action, note}
func (h *handlers) localFirewallCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	port := jsonInt(body["port"])
	if port <= 0 || port > 65535 {
		writeError(w, http.StatusBadRequest, "端口需在 1-65535 之间")
		return
	}
	proto := strings.ToLower(strings.TrimSpace(mapStr(body, "protocol", "tcp")))
	if proto != "tcp" && proto != "udp" {
		writeError(w, http.StatusBadRequest, "协议仅支持 tcp / udp")
		return
	}
	action := strings.ToLower(strings.TrimSpace(mapStr(body, "action", "open")))
	if action != "open" && action != "close" {
		writeError(w, http.StatusBadRequest, "动作仅支持 open / close")
		return
	}
	note := truncateRunes(strings.TrimSpace(mapStr(body, "note", "")), 64)

	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO local_port_rules (port, protocol, action, note, created_at) VALUES (?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE action = VALUES(action), note = VALUES(note)`,
		port, proto, action, note, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存规则失败："+err.Error())
		return
	}
	if err := h.applyPortRules(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	verb := "开放"
	if action == "close" {
		verb = "关闭"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("已%s %d/%s", verb, port, proto),
	})
}

// DELETE /api/local/firewall/{id}
func (h *handlers) localFirewallDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(pathParam(r, "id"), 10, 64)
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "缺少规则 ID")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `DELETE FROM local_port_rules WHERE id = ?`, id); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if err := h.applyPortRules(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "已删除规则"})
}
