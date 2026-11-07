package api

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
)

// 本机端口规则：支持来源网段（原 IP/CIDR）→ 目的端口（可范围）。
// 规则落在独立链 PP_PORTS，挂在 INPUT 首条。

const (
	portChain  = "PP_PORTS"
	portScript = "/usr/local/sbin/pp-port-rules.sh"
	portUnit   = "/etc/systemd/system/pp-port-rules.service"
)

type portRule struct {
	ID        int64  `json:"id"`
	Port      int64  `json:"port"`
	PortEnd   int64  `json:"port_end"`
	Protocol  string `json:"protocol"`
	Action    string `json:"action"`
	Note      string `json:"note"`
	SrcCIDR   string `json:"src_cidr"`
	Listening bool   `json:"listening"`
}

func (h *handlers) localPortRules(r *http.Request) ([]portRule, error) {
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, port, COALESCE(port_end,0), COALESCE(protocol,'tcp'), COALESCE(action,'internal'),
		        COALESCE(note,''), COALESCE(src_cidr,'')
		   FROM local_port_rules ORDER BY port ASC, protocol ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []portRule{}
	for rows.Next() {
		var p portRule
		if rows.Scan(&p.ID, &p.Port, &p.PortEnd, &p.Protocol, &p.Action, &p.Note, &p.SrcCIDR) == nil {
			out = append(out, p)
		}
	}
	return out, nil
}

// dportExpr iptables 的端口表达式：单端口 8080 / 范围 8000:8100
func (p portRule) dportExpr() string {
	if p.PortEnd > p.Port {
		return fmt.Sprintf("%d:%d", p.Port, p.PortEnd)
	}
	return strconv.FormatInt(p.Port, 10)
}

// srcExpr 来源匹配：空 = 任意来源
func (p portRule) srcExpr() string {
	s := strings.TrimSpace(p.SrcCIDR)
	if s == "" {
		return ""
	}
	return " -s " + s
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
		dport := p.dportExpr()
		src := p.srcExpr()
		line := func(target, extra string) string {
			return fmt.Sprintf("  $BIN -A $CHAIN -p %s%s%s -j %s\n", proto, extra, src, target)
		}
		switch p.Action {
		case "close":
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s%s -j DROP\n", proto, dport, src))
		case "loopback":
			// 仅本机回环可访问
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s -s 127.0.0.0/8 -j ACCEPT\n", proto, dport))
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s -s ::1/128 -j ACCEPT 2>/dev/null || true\n", proto, dport))
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s%s -j DROP\n", proto, dport, src))
		case "internal":
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s -s 100.96.0.0/12 -j ACCEPT\n", proto, dport))
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s -s 127.0.0.0/8 -j ACCEPT\n", proto, dport))
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s%s -j DROP\n", proto, dport, src))
		default:
			_ = line
			sb.WriteString(fmt.Sprintf("  $BIN -A $CHAIN -p %s -m multiport --dports %s%s -j ACCEPT\n", proto, dport, src))
		}
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

// normalizeCIDR 校验并规范化来源（允许单 IP 或 CIDR）。
func normalizeCIDR(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if !strings.Contains(s, "/") {
		if ip := net.ParseIP(s); ip != nil {
			if ip.To4() != nil {
				return s + "/32", nil
			}
			return s + "/128", nil
		}
		return "", fmt.Errorf("来源地址不合法：%s", raw)
	}
	if _, _, err := net.ParseCIDR(s); err != nil {
		return "", fmt.Errorf("来源网段不合法：%s", raw)
	}
	return s, nil
}

// parsePortSpec 解析 "8080" 或 "8000-8100"。
func parsePortSpec(raw string) (int64, int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, 0, fmt.Errorf("端口为空")
	}
	if strings.Contains(s, "-") {
		parts := strings.SplitN(s, "-", 2)
		a, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		b, err2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err1 != nil || err2 != nil || a <= 0 || b <= 0 || a > b || b > 65535 {
			return 0, 0, fmt.Errorf("端口范围不合法：%s", raw)
		}
		return a, b, nil
	}
	p, err := strconv.ParseInt(s, 10, 64)
	if err != nil || p <= 0 || p > 65535 {
		return 0, 0, fmt.Errorf("端口不合法：%s", raw)
	}
	return p, 0, nil
}

func listeningPorts() map[int64]string {
	out := map[int64]string{}
	bin, err := exec.LookPath("ss")
	if err != nil {
		return out
	}
	raw, err := exec.Command(bin, "-tlnp").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		addr := fields[3]
		idx := strings.LastIndex(addr, ":")
		if idx < 0 {
			continue
		}
		p, convErr := strconv.ParseInt(addr[idx+1:], 10, 64)
		if convErr != nil || p <= 0 {
			continue
		}
		proc := ""
		if i := strings.Index(line, "users:(("); i >= 0 {
			rest := line[i+len("users:(("):]
			if j := strings.Index(rest, "\""); j > 0 {
				proc = rest[:j]
			}
			proc = strings.Trim(proc, "\" ")
		}
		out[p] = proc
	}
	return out
}

// portLabel 给常见端口一个可读标签，优先显示进程名。
func portLabel(port int64, proc string) string {
	if proc != "" {
		return proc
	}
	switch port {
	case 22:
		return "OpenSSH"
	case 53:
		return "DNS"
	case 80:
		return "面板 HTTP"
	case 443:
		return "面板 HTTPS"
	case 3306:
		return "MySQL"
	case 33060:
		return "MySQL X Protocol"
	case 8080:
		return "内网 Agent"
	}
	return ""
}

// GET /api/local/firewall
func (h *handlers) localFirewallList(w http.ResponseWriter, r *http.Request) {
	rules, err := h.localPortRules(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取端口规则失败")
		return
	}
	live := listeningPorts()
	for i := range rules {
		rules[i].Listening = false
		if rules[i].Protocol != "tcp" {
			continue
		}
		for port := rules[i].Port; port <= maxInt64(rules[i].Port, rules[i].PortEnd); port++ {
			if _, ok := live[port]; ok {
				rules[i].Listening = true
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rules})
}

func maxInt64(a, b int64) int64 {
	if b > a {
		return b
	}
	return a
}

// POST /api/local/firewall
func (h *handlers) localFirewallCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	portSpec := mapStr(body, "port_spec", "")
	if portSpec == "" {
		portSpec = strconv.FormatInt(jsonInt(body["port"]), 10)
		if pe := jsonInt(body["port_end"]); pe > 0 {
			portSpec = fmt.Sprintf("%d-%d", jsonInt(body["port"]), pe)
		}
	}
	port, portEnd, err := parsePortSpec(portSpec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	proto := strings.ToLower(strings.TrimSpace(mapStr(body, "protocol", "tcp")))
	if proto != "tcp" && proto != "udp" {
		writeError(w, http.StatusBadRequest, "协议仅支持 tcp / udp")
		return
	}
	action := strings.ToLower(strings.TrimSpace(mapStr(body, "action", "internal")))
	if !validPortAction(action) {
		writeError(w, http.StatusBadRequest, "动作仅支持 open / internal / loopback / close")
		return
	}
	src, err := normalizeCIDR(mapStr(body, "src_cidr", ""))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	note := truncateRunes(strings.TrimSpace(mapStr(body, "note", "")), 64)

	_, err = h.db.ExecContext(r.Context(),
		`INSERT INTO local_port_rules (port, port_end, protocol, action, note, src_cidr, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE action = VALUES(action), note = VALUES(note)`,
		port, portEnd, proto, action, note, src, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存规则失败："+err.Error())
		return
	}
	if err := h.applyPortRules(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	verb := portActionLabel(action)
	target := strconv.FormatInt(port, 10)
	if portEnd > port {
		target = fmt.Sprintf("%d-%d", port, portEnd)
	}
	if src != "" {
		target = src + " → " + target
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": fmt.Sprintf("已%s %s/%s", verb, target, proto),
	})
}

// PUT /api/local/firewall/{id}：编辑已有规则
func (h *handlers) localFirewallUpdate(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(pathParam(r, "id"), 10, 64)
	if id <= 0 {
		writeError(w, http.StatusBadRequest, "缺少规则 ID")
		return
	}
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	portSpec := mapStr(body, "port_spec", "")
	if portSpec == "" {
		portSpec = strconv.FormatInt(jsonInt(body["port"]), 10)
		if pe := jsonInt(body["port_end"]); pe > 0 {
			portSpec = fmt.Sprintf("%d-%d", jsonInt(body["port"]), pe)
		}
	}
	port, portEnd, err := parsePortSpec(portSpec)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	proto := strings.ToLower(strings.TrimSpace(mapStr(body, "protocol", "tcp")))
	if proto != "tcp" && proto != "udp" {
		writeError(w, http.StatusBadRequest, "协议仅支持 tcp / udp")
		return
	}
	action := strings.ToLower(strings.TrimSpace(mapStr(body, "action", "internal")))
	if !validPortAction(action) {
		writeError(w, http.StatusBadRequest, "动作仅支持 open / internal / loopback / close")
		return
	}
	src, err := normalizeCIDR(mapStr(body, "src_cidr", ""))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	note := truncateRunes(strings.TrimSpace(mapStr(body, "note", "")), 64)

	if _, err := h.db.ExecContext(r.Context(),
		`UPDATE local_port_rules SET port=?, port_end=?, protocol=?, action=?, note=?, src_cidr=? WHERE id=?`,
		port, portEnd, proto, action, note, src, id); err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败："+err.Error())
		return
	}
	if err := h.applyPortRules(r); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "规则已更新"})
}

// POST /api/local/firewall/sync：把当前监听端口导入为规则（默认仅内网）。
func (h *handlers) localFirewallSync(w http.ResponseWriter, r *http.Request) {
	live := listeningPorts()
	existing := map[int64]bool{}
	if rows, err := h.localPortRules(r); err == nil {
		for _, x := range rows {
			if x.Protocol == "tcp" {
				existing[x.Port] = true
				if x.PortEnd > x.Port {
					for p := x.Port; p <= x.PortEnd; p++ {
						existing[p] = true
					}
				}
			}
		}
	}
	added := 0
	for port, proc := range live {
		if existing[port] {
			continue
		}
		// 22 端口交由「SSH 安全防护」管理，端口规则不接管，避免双重 DROP 锁死运维通道
		if port == 22 {
			continue
		}
		note := portLabel(port, proc)
		if len(note) > 60 {
			note = note[:60]
		}
		if _, err := h.db.ExecContext(r.Context(),
			`INSERT INTO local_port_rules (port, port_end, protocol, action, note, src_cidr, created_at)
			 VALUES (?, 0, 'tcp', 'internal', ?, '', ?)
			 ON DUPLICATE KEY UPDATE note = VALUES(note)`,
			port, note, nowMillis()); err == nil {
			added++
		}
	}
	if added > 0 {
		if err := h.applyPortRules(r); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "added": added,
		"message": fmt.Sprintf("已同步 %d 个监听端口", added),
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

func validPortAction(a string) bool {
	switch a {
	case "open", "internal", "loopback", "close":
		return true
	}
	return false
}

func portActionLabel(a string) string {
	switch a {
	case "open":
		return "对外开放"
	case "internal":
		return "设为仅内网"
	case "loopback":
		return "设为仅本机"
	case "close":
		return "关闭"
	}
	return a
}
