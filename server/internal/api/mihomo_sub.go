package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// GET /sub/mihomo/{uuid}
// 面向用户端的 mihomo 订阅：汇总该 UUID 有权限的全部 mihomo 绑定节点，生成一份 YAML。
func (h *handlers) mihomoSubscribe(w http.ResponseWriter, r *http.Request) {
	uuid := strings.ToLower(strings.TrimSpace(pathParam(r, "uuid")))
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式无效")
		return
	}
	var n int
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM (SELECT uuid FROM uuid_info UNION SELECT uuid FROM configs) t WHERE uuid = ?`, uuid).Scan(&n)
	if n == 0 {
		/* 兼容仅在 sing-box 模板 users_json 中存在、尚未登记 uuid_info 的用户 */
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM singbox_templates WHERE users_json LIKE CONCAT('%', ?, '%')`, uuid).Scan(&n)
	}
	if n == 0 {
		http.NotFound(w, r)
		return
	}

	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT n.id, n.name, n.server_id, n.template_id, COALESCE(n.uuid_list,'[]') AS uuid_list,
		        COALESCE(n.protocols,'[]') AS protocols
		   FROM mihomo_nodes n WHERE n.enabled = 1 ORDER BY n.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询 mihomo 绑定失败")
		return
	}

	var proxies []map[string]any
	var baseTemplate string
	names := map[string]bool{}
	for _, row := range rows {
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		if len(list) > 0 && !mihomoContains(list, uuid) {
			continue
		}
		var nodeID int64
		if h.db.QueryRowContext(r.Context(),
			`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`,
			mapInt(row, "server_id", 0)).Scan(&nodeID) != nil || nodeID == 0 {
			continue
		}
		tpl, node, _, _, msg := h.loadSingboxNodeFull(r.Context(), nodeID)
		if msg != "" {
			continue
		}
		users := singboxParseUsers(currentTemplateUsers(tpl))
		filtered := make([]sbUser, 0, 1)
		for _, u := range users {
			if strings.EqualFold(strings.TrimSpace(u.UUID), uuid) {
				filtered = append(filtered, u)
			}
		}
		if len(filtered) == 0 {
			continue
		}
		rawLinks := singboxClientLinks(tpl, singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}, filtered)
		protoSet := map[string]bool{}
		for _, p := range mihomoParseProtocols(mapStr(row, "protocols", "[]")) {
			protoSet[p] = true
		}
		links := make([]string, 0, len(rawLinks))
		for _, item := range rawLinks {
			if !mihomoLinkAllowed(item, protoSet) {
				continue
			}
			if s := mapStr(item, "link", ""); s != "" {
				links = append(links, s)
			}
		}
		if len(links) == 0 {
			continue
		}
		part, _, _, _ := h.buildClashProxies(r.Context(), links)
		for _, p := range part {
			if fmt.Sprint(p["type"]) == "direct" {
				continue
			}
			base := fmt.Sprint(p["name"])
			name := base
			k := 1
			for names[name] {
				name = fmt.Sprintf("%s-%d", base, k)
				k++
			}
			p["name"] = name
			names[name] = true
			proxies = append(proxies, p)
		}
		if baseTemplate == "" {
			var content string
			if h.db.QueryRowContext(r.Context(),
				`SELECT COALESCE(content,'') FROM mihomo_templates WHERE id = ?`, mapInt(row, "template_id", 0)).Scan(&content) == nil {
				baseTemplate = content
			}
		}
	}

	if len(proxies) == 0 || strings.TrimSpace(baseTemplate) == "" {
		writeError(w, http.StatusNotFound, "该用户暂无可用的 mihomo 节点")
		return
	}
	proxies = append([]map[string]any{{"name": "直连", "type": "direct"}}, proxies...)
	finalConfig := injectClashProxies(baseTemplate, proxies)
	if strings.Contains(finalConfig, "{{PROXY_NAMES}}") {
		namesList := make([]string, 0, len(proxies))
		for _, p := range proxies {
			if fmt.Sprint(p["type"]) == "direct" {
				continue
			}
			namesList = append(namesList, "      - "+strconv.Quote(fmt.Sprint(p["name"])))
		}
		if len(namesList) == 0 {
			namesList = append(namesList, "      - DIRECT")
		}
		finalConfig = strings.ReplaceAll(finalConfig, "{{PROXY_NAMES}}", strings.Join(namesList, "\n"))
	}

	filename := fmt.Sprintf("mihomo-%s-%s.yaml", uuid[:8], time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/yaml;charset=UTF-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Total-Proxies", strconv.Itoa(len(proxies)-1))
	w.Header().Set("X-Mihomo-Bindings", strconv.Itoa(len(rows)))
	w.Header().Set("X-Generated-Date", time.Now().UTC().Format(time.RFC3339))
	_, _ = w.Write([]byte(finalConfig))
}

func mihomoParseProtocols(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	for _, p := range list {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func mihomoContains(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
