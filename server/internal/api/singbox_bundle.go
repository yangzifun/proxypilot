package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

const singboxDemoUUID = "00000000-0000-4000-8000-000000000000"

func singboxFileView(f map[string]any) map[string]any {
	return map[string]any{
		"key": mapStr(f, "key", ""), "name": mapStr(f, "name", ""), "engine": mapStr(f, "engine", ""),
		"path": mapStr(f, "path", ""), "service_name": mapStr(f, "service_name", ""),
		"check_cmd": mapStr(f, "check_cmd", ""),
		"content": func() string {
			if s, ok := f["content"].(string); ok {
				return s
			}
			return ""
		}(),
		"node_config_id": int64(0), "version": int64(0), "changed": false,
	}
}

func versionAtLeast(ver string, major, minor int) bool {
	m := parseAgentVersion(ver)
	if len(m) < 2 {
		return true // 解析不出就不拦
	}
	return m[0] > major || (m[0] == major && m[1] >= minor)
}

func serverComponentVersion(row serverRow, name string) string {
	if len(row.LastMetrics) == 0 {
		return ""
	}
	var m struct {
		Versions map[string]string `json:"versions"`
	}
	if json.Unmarshal(row.LastMetrics, &m) != nil {
		return ""
	}
	return strings.TrimSpace(m.Versions[name])
}

// singboxRenderBundle 复刻 Worker：校验 + 生成待下发文件。
func singboxRenderBundle(tpl *singboxTemplate, node singboxRenderNode, srv serverRow) ([]map[string]any, map[string]int, []map[string]string, string) {
	users := singboxParseUsers(currentTemplateUsers(tpl))
	for _, u := range users {
		if strings.ToLower(u.UUID) == singboxDemoUUID {
			return nil, nil, nil, "检测到占位 UUID（00000000-0000-4000-8000-000000000000）：请改用真实 UUID 或从「UUID 授权」里授权，不要用示例值上线"
		}
	}
	if tpl.EnableReality != 0 {
		rk := strings.TrimRight(strings.TrimSpace(tpl.RealityPrivateKey), "=")
		if len(rk) != 43 {
			return nil, nil, nil, "REALITY 私钥无效（应为 32 字节 base64url），请重新生成模板密钥"
		}
	}
	if tpl.EnableSS != 0 {
		if raw, ok := decodeAnyBase64(tpl.SSServerKey); !ok || len(raw) != 32 {
			return nil, nil, nil, "SS2022 服务端密钥必须是 32 字节 base64，请重新生成模板密钥"
		}
		if len(users) > 0 {
			hasSS := false
			for _, u := range users {
				if u.SSPassword != "" {
					hasSS = true
					break
				}
			}
			if !hasSS {
				return nil, nil, nil, "启用 shadowsocks 时，users_json 里至少要有一个用户带 ss_password（多用户各自独立密钥）"
			}
		}
	}
	if tpl.EnableVLWS != 0 && strings.TrimSpace(node.Domain) == "" {
		return nil, nil, nil, "启用了 vless-ws 时必须填写域名（证书与伪装站都依赖它）"
	}
	if tpl.EnableSS != 0 && strings.TrimSpace(tpl.SSServerKey) == "" {
		return nil, nil, nil, "启用 shadowsocks 时必须填写服务器密钥 ss_server_key"
	}
	if tpl.EnableReality != 0 && strings.TrimSpace(tpl.RealityPrivateKey) == "" {
		return nil, nil, nil, "启用 REALITY 时必须填写私钥 reality_private_key"
	}

	ports := singboxPorts(tpl)
	files := []map[string]any{}

	sbPath := firstNonEmpty(strings.TrimSpace(node.SBPath), "/etc/sing-box/sb.json")
	if tpl.engineOn("singbox") {
		// 只有节点回报的版本是自建构建（-proxypilot）时才写 v2ray_api：
		// 官方 sing-box 没有 with_v2ray_api 标签，提前写会让 sing-box check 失败。
		// 上线顺序：先切版本（第一次下发），节点回报新版本后再下发一次（第二次带 v2ray_api）。
		userStats := singboxUserStatsCapable(serverComponentVersion(srv, "sing-box"))
		files = append(files, map[string]any{
			"key": "sb", "name": "sb.json", "engine": "sing-box",
			"path":         sbPath,
			"service_name": firstNonEmpty(node.ServiceSBName, "sing-box"),
			"check_cmd":    "sing-box check -c " + sbPath,
			"content":      mustJSON(singboxRenderSb(tpl, users, ports, node.StatsSecret, userStats, node.CDNDomain)),
		})
	}
	if tpl.EnableMasque != 0 {
		if len(users) == 0 {
			return nil, nil, nil, "启用 MASQUE 需要至少一个用户（endpoint 的 HTTP Basic 认证），请先授权 UUID 或填写模板用户"
		}
		if ver := serverComponentVersion(srv, "sing-box"); ver != "" && !versionAtLeast(ver, 1, 15) {
			return nil, nil, nil, "MASQUE 需要 sing-box >= 1.15.0（masque-server endpoint），当前节点是 " + ver + "；请升级 sing-box，或暂时关闭该模板的 MASQUE"
		}
	}
	outUsers := make([]map[string]string, 0, len(users))
	for _, u := range users {
		outUsers = append(outUsers, map[string]string{"name": u.Name, "uuid": u.UUID})
	}
	return files, ports, outUsers, ""
}

// singboxUserStatsCapable 判断该 sing-box 是否为带 v2ray_api 的自建构建。
// 官方 release 没有 with_v2ray_api 编译标签，写入 experimental.v2ray_api 会让
// 节点的 `sing-box check` 直接失败；所以只对版本号带 -proxypilot 的自建构建启用按用户统计。
func singboxUserStatsCapable(ver string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(ver)), "proxypilot")
}

func mustJSON(v any) string {	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(b) + "\n"
}

// currentTemplateUsers 模板用户来自 users_json（预览/渲染时使用）。
func currentTemplateUsers(tpl *singboxTemplate) []sbUser {
	if strings.TrimSpace(tpl.UsersJSON) == "" {
		return nil
	}
	var users []sbUser
	_ = json.Unmarshal([]byte(tpl.UsersJSON), &users)
	return users
}

type singboxRenderNode struct {
	Name             string
	Domain           string
	CDNDomain        string
	SBPath           string
	CaddyPath        string
	ServiceSBName    string
	ServiceCaddyName string
	// StatsSecret 非空时在 sb.json 里注入 experimental（clash_api / v2ray_api）
	StatsSecret string
}

// POST /api/singbox/preview
func (h *handlers) singboxPreview(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	tplBody := body
	if v, ok := body["template"].(map[string]any); ok {
		tplBody = v
	}
	nodeBody := body
	if v, ok := body["node"].(map[string]any); ok {
		nodeBody = v
	}
	tplInput, msg := normalizeSingboxTemplateInput(tplBody)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	nodeInput, msg := normalizeSingboxNodeInput(nodeBody)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	serverID := jsonInt(nodeInput["server_id"])
	row, err := h.fetchServerRow(r.Context(), serverID)
	if err != nil {
		writeError(w, http.StatusNotFound, "目标服务器不存在")
		return
	}

	tpl := inputToTemplate(tplInput)
	node := inputToNode(nodeInput)
	files, ports, users, msg := singboxRenderBundle(tpl, node, row)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	view := make([]map[string]any, 0, len(files))
	for _, f := range files {
		view = append(view, singboxFileView(f))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "ports": ports, "users": users, "files": view,
	})
}

func inputToTemplate(in map[string]any) *singboxTemplate {
	b, _ := json.Marshal(in)
	var tpl singboxTemplate
	_ = json.Unmarshal(b, &tpl)
	return &tpl
}

func inputToNode(in map[string]any) singboxRenderNode {
	return singboxRenderNode{
		Name:          jsonStr(in["name"]),
		Domain:        jsonStr(in["domain"]),
		CDNDomain:     jsonStr(in["cdn_domain"]),
		SBPath:        jsonStr(in["sb_path"]),
		ServiceSBName: jsonStr(in["service_sb"]),
	}
}
