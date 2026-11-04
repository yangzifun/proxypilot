package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// GET /api/mihomo/bin/{arch}  从面板本地目录分发 mihomo 二进制（gz）
func (h *handlers) mihomoBin(w http.ResponseWriter, r *http.Request) {
	arch := strings.ToLower(strings.TrimSpace(pathParam(r, "arch")))
	if arch != "amd64" && arch != "arm64" {
		http.NotFound(w, r)
		return
	}
	dir := strings.TrimSpace(os.Getenv("PP_MIHOMO_DIST_DIR"))
	if dir == "" {
		dir = "/opt/proxypilot/mihomo-dist"
	}
	file := filepath.Join(dir, "mihomo-linux-"+arch+".gz")
	f, err := os.Open(file)
	if err != nil {
		writeError(w, http.StatusNotFound, "未找到 mihomo 二进制包（"+arch+"）")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取二进制失败")
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", st.Size()))
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, st.Name(), st.ModTime(), f)
}

// POST /api/mihomo/nodes/{id}/apply  生成配置并下发到服务器（安装 mihomo + 写配置 + 重启）
func (h *handlers) mihomoNodeApplyServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	var (
		name       string
		serverID   int64
		uuidRaw    string
		mode       string
		serverRaw  string
		portBase   int64
		configPath string
		service    string
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT n.name, n.server_id, COALESCE(n.uuid_list,'[]'), COALESCE(n.port_base,0),
		        COALESCE(n.config_path,'/etc/mihomo/config.yaml'), COALESCE(n.service,'mihomo'),
		        COALESCE(t.mode,'subscription'), COALESCE(t.server_json,'{}')
		   FROM mihomo_nodes n LEFT JOIN mihomo_templates t ON t.id = n.template_id
		  WHERE n.id = ? LIMIT 1`, id).
		Scan(&name, &serverID, &uuidRaw, &portBase, &configPath, &service, &mode, &serverRaw)
	if err != nil {
		writeError(w, http.StatusNotFound, "绑定不存在")
		return
	}
	if mode != "server" {
		writeError(w, http.StatusBadRequest, "该模板不是服务端模板")
		return
	}
	if configPath == "" {
		configPath = "/etc/mihomo/config.yaml"
	}
	if service == "" {
		service = "mihomo"
	}
	cfg := parseMihomoServerConfig(serverRaw)
	if portBase > 0 {
		cfg.PortBase = int(portBase)
	}
	yamlText, _, msg := renderMihomoServer(cfg, mihomoParseUUIDs(uuidRaw))
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	srv, err := h.fetchAgentServer(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	base := strings.TrimRight(strings.TrimSpace(h.cfg.PublicBaseURL), "/")
	if base == "" {
		base = strings.TrimRight(requestOrigin(r), "/")
	}
	script := buildMihomoApplyScript(base, configPath, service, yamlText)
	payload := map[string]any{"command": script, "timeout": 300}
	taskID, dup, qmsg := h.agentQueueTask(r.Context(), srv, "exec", 0, payload, "admin")
	if qmsg != "" {
		writeError(w, http.StatusBadRequest, qmsg)
		return
	}
	_, _ = h.db.ExecContext(r.Context(), `UPDATE mihomo_nodes SET last_apply_at = ? WHERE id = ?`, nowMillis(), id)
	h.mihomoAudit(r, "mihomo-apply", serverID, fmt.Sprintf("下发 mihomo 绑定 %s（task %d）", name, taskID))
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "task_id": taskID, "duplicated": dup,
		"message": fmt.Sprintf("已排入下发任务 #%d，可在「任务」中查看进度", taskID),
	})
}

func buildMihomoApplyScript(base, configPath, service, yamlText string) string {
	unitPath := "/etc/systemd/system/" + service + ".service"
	return `set -e
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) A=amd64 ;;
  aarch64|arm64) A=arm64 ;;
  *) echo "unsupported arch: $ARCH"; exit 2 ;;
esac
BIN=/usr/local/bin/mihomo
mkdir -p /etc/mihomo
if ! "$BIN" -v >/dev/null 2>&1; then
  echo "downloading mihomo ($A)"
  curl -fsSL -m 240 -o /tmp/mihomo.gz "` + base + `/api/mihomo/bin/$A"
  gunzip -c /tmp/mihomo.gz > /tmp/mihomo.bin
  install -m 0755 /tmp/mihomo.bin "$BIN"
  rm -f /tmp/mihomo.gz /tmp/mihomo.bin
fi
cat > ` + configPath + ` <<'PP_MIHOMO_EOF'
` + yamlText + `PP_MIHOMO_EOF
cat > ` + unitPath + ` <<'PP_UNIT_EOF'
[Unit]
Description=mihomo (ProxyPilot)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/mihomo -d /etc/mihomo
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
PP_UNIT_EOF
systemctl daemon-reload
systemctl enable ` + service + ` >/dev/null 2>&1 || true
echo "checking config"
"$BIN" -d /etc/mihomo -t
systemctl restart ` + service + `
sleep 2
systemctl is-active ` + service + `
"$BIN" -v | head -1
echo "done"
`
}
