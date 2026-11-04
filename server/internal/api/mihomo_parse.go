package api

import (
	"fmt"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

// POST /api/mihomo/templates/parse
// 解析粘贴/导入的 mihomo YAML，返回常用字段与统计，供模板编辑器回填与提示。
func (h *handlers) mihomoTemplateParse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "内容为空")
		return
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(body.Content), &doc); err != nil {
		writeError(w, http.StatusBadRequest, "YAML 语法错误："+err.Error())
		return
	}
	if doc == nil {
		writeError(w, http.StatusBadRequest, "内容不是有效的 YAML 对象")
		return
	}
	str := func(key string) string {
		if v, ok := doc[key]; ok && v != nil {
			return strings.TrimSpace(fmt.Sprint(v))
		}
		return ""
	}
	count := func(key string) int {
		if v, ok := doc[key]; ok {
			if list, ok2 := v.([]any); ok2 {
				return len(list)
			}
		}
		return 0
	}
	fields := map[string]any{
		"mixed-port":          str("mixed-port"),
		"mode":                str("mode"),
		"log-level":           str("log-level"),
		"external-controller": str("external-controller"),
		"allow-lan":           strings.EqualFold(str("allow-lan"), "true"),
		"ipv6":                strings.EqualFold(str("ipv6"), "true"),
	}
	var warnings []string
	if _, ok := doc["proxies"]; !ok {
		warnings = append(warnings, "缺少 proxies:，生成时无法注入节点")
	}
	if _, ok := doc["proxy-groups"]; !ok {
		warnings = append(warnings, "缺少 proxy-groups:，客户端无分组可选")
	}
	if _, ok := doc["rules"]; !ok {
		warnings = append(warnings, "缺少 rules:，需 MATCH 兜底规则")
	}
	/* 解析 listeners → 服务端模板参数（从现有配置导入） */
	serverCfg := map[string]any{}
	serverProtocols := []string{}
	if rawList, ok := doc["listeners"].([]any); ok && len(rawList) > 0 {
		seen := map[string]bool{}
		basePort := 0
		offsets := map[string]int{"hy2": 1, "ss": 2, "anytls": 3, "tuic": 4, "vless_reality": 5, "vless_ws": 6, "vmess": 7, "trojan": 8, "socks": 9, "http": 10}
		for _, item := range rawList {
			lm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			typ := strings.ToLower(strings.TrimSpace(fmt.Sprint(lm["type"])))
			port := 0
			switch v := lm["port"].(type) {
			case int:
				port = v
			case int64:
				port = int(v)
			case float64:
				port = int(v)
			case string:
				fmt.Sscanf(v, "%d", &port)
			}
			key := ""
			switch typ {
			case "vless":
				if _, hasReality := lm["reality-config"]; hasReality {
					key = "vless_reality"
					if rc, ok := lm["reality-config"].(map[string]any); ok {
						serverCfg["reality_dest"] = strings.TrimSpace(fmt.Sprint(rc["dest"]))
						serverCfg["reality_private_key"] = strings.TrimSpace(fmt.Sprint(rc["private-key"]))
						if sid, ok := rc["short-id"].([]any); ok && len(sid) > 0 {
							serverCfg["reality_short_id"] = strings.TrimSpace(fmt.Sprint(sid[0]))
						}
						if sn, ok := rc["server-names"].([]any); ok {
							names := make([]string, 0, len(sn))
							for _, x := range sn {
								names = append(names, strings.TrimSpace(fmt.Sprint(x)))
							}
							serverCfg["reality_server_names"] = names
						}
					}
				} else {
					key = "vless_ws"
				}
			case "vmess":
				key = "vmess"
			case "trojan":
				key = "trojan"
			case "anytls":
				key = "anytls"
			case "hysteria2", "hy2":
				key = "hy2"
			case "tuic":
				key = "tuic"
			case "socks":
				key = "socks"
			case "http":
				key = "http"
			case "shadowsocks", "ss":
				key = "ss"
			}
			if key == "" {
				continue
			}
			if !seen[key] {
				seen[key] = true
				serverProtocols = append(serverProtocols, key)
			}
			if ws := strings.TrimSpace(fmt.Sprint(lm["ws-path"])); ws != "<nil>" && ws != "" {
				serverCfg["ws_path"] = ws
			}
			if c := strings.TrimSpace(fmt.Sprint(lm["certificate"])); c != "<nil>" && c != "" {
				serverCfg["cert_path"] = c
			}
			if k := strings.TrimSpace(fmt.Sprint(lm["private-key"])); k != "<nil>" && k != "" {
				serverCfg["key_path"] = k
			}
			if port > 0 {
				cand := port - offsets[key]
				if basePort == 0 || cand < basePort {
					basePort = cand
				}
			}
		}
		if basePort > 0 {
			serverCfg["port_base"] = basePort
		}
		if len(serverProtocols) > 0 {
			serverCfg["protocols"] = serverProtocols
		}
		serverCfg["listeners"] = rawList
		if s := strings.TrimSpace(fmt.Sprint(doc["secret"])); s != "<nil>" && s != "" {
			serverCfg["secret"] = s
		}
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"server_cfg": serverCfg,
			"mode": func() string {
				if len(serverProtocols) > 0 {
					return "server"
				}
				return "subscription"
			}(),
			"listeners":    len(serverProtocols),
			"fields":       fields,
			"proxy_count":  count("proxies"),
			"group_count":  count("proxy-groups"),
			"rule_count":   count("rules"),
			"top_keys":     keys,
			"warnings":     warnings,
			"content_size": len(body.Content),
		},
	})
}
