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
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
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
