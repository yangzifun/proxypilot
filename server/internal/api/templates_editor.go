package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// =====================================================================
//  Clash / Mihomo 订阅模板编辑器（后端）
//
//  设计（见 myworkspace/reports/template-ui-editor-plan.md）：
//   - 草稿 / 已发布内容都存 DB，发布时才写回原 R2 URL（保持旧客户端兼容）；
//   - YAML 用 yaml.v3 的 Node 解析，区段 patch 只替换目标区段，尽量保留注释/锚点/别名；
//   - `proxies` 是系统注入位，编辑器禁止写入真实节点；
//   - 运行时 /clash-config 优先 published_content，为空才回退 URL。
// =====================================================================

const (
	templateEditorMaxBytes    = 1 << 20 // 1MB
	templateEditorMaxNodes    = 200_000
	templateEditorMaxAliases  = 500
	templateEditorMaxDepth    = 200
	templateURLContentTTL     = 5 * time.Minute
	templateURLMaxBytes       = 4 << 20 // 4MB（与运行时原逻辑一致）
	templateURLUserAgent      = "ProxyPilot-Template-Editor"
	templateManagedR2HostName = "s3.proxypilot.yangzifun.org"
	templateManagedR2Prefix   = "/rule/configs/"
)

// templateNodeStats YAML 文档的规模/结构统计（同时用于安全限制与摘要）。
type templateNodeStats struct {
	Nodes    int
	Anchors  int
	Aliases  int
	Comments int
}

func (s templateNodeStats) summary(root *yaml.Node) templateContentSummary {
	sum := templateContentSummary{SizeBytes: 0}
	if root != nil && root.Kind == yaml.MappingNode {
		sum.TopKeys = len(root.Content) / 2
		for i := 0; i+1 < len(root.Content); i += 2 {
			key := root.Content[i].Value
			val := root.Content[i+1]
			switch key {
			case "proxy-groups":
				sum.ProxyGroups = seqLen(val)
			case "rules":
				sum.Rules = seqLen(val)
			case "rule-providers":
				sum.RuleProviders = mapLen(val)
			case "proxies":
				sum.Proxies = seqLen(val)
			}
		}
	}
	sum.Anchors = s.Anchors
	sum.Aliases = s.Aliases
	sum.Comments = s.Comments
	return sum
}

// templateContentSummary 模板内容摘要（前端展示/发布确认用）。
type templateContentSummary struct {
	TopKeys       int `json:"top_keys"`
	ProxyGroups   int `json:"proxy_groups"`
	Rules         int `json:"rules"`
	RuleProviders int `json:"rule_providers"`
	Proxies       int `json:"proxies"`
	Anchors       int `json:"anchors"`
	Aliases       int `json:"aliases"`
	Comments      int `json:"comments"`
	SizeBytes     int `json:"size_bytes"`
}

func seqLen(n *yaml.Node) int {
	if n != nil && n.Kind == yaml.SequenceNode {
		return len(n.Content)
	}
	return 0
}

func mapLen(n *yaml.Node) int {
	if n != nil && n.Kind == yaml.MappingNode {
		return len(n.Content) / 2
	}
	return 0
}

// scanTemplateNode 递归扫描节点，统计规模并做安全校验（自定义 tag / 深度 / 别名数）。
func scanTemplateNode(n *yaml.Node, depth int, st *templateNodeStats) error {
	if n == nil {
		return nil
	}
	if depth > templateEditorMaxDepth {
		return errors.New("YAML 嵌套过深")
	}
	st.Nodes++
	if st.Nodes > templateEditorMaxNodes {
		return fmt.Errorf("YAML 节点过多（上限 %d）", templateEditorMaxNodes)
	}
	if tag := strings.TrimSpace(n.Tag); strings.HasPrefix(tag, "!") && !strings.HasPrefix(tag, "!!") {
		return fmt.Errorf("禁止自定义 YAML tag：%s", tag)
	}
	if n.Anchor != "" {
		st.Anchors++
	}
	if n.Kind == yaml.AliasNode {
		st.Aliases++
		if st.Aliases > templateEditorMaxAliases {
			return fmt.Errorf("YAML 别名过多（上限 %d）", templateEditorMaxAliases)
		}
	}
	if n.HeadComment != "" || n.LineComment != "" || n.FootComment != "" {
		st.Comments++
	}
	for _, child := range n.Content {
		if err := scanTemplateNode(child, depth+1, st); err != nil {
			return err
		}
	}
	return nil
}

// topLevelDuplicateKeys 返回顶层 map 中重复出现的键名（顺序稳定）。
func topLevelDuplicateKeys(root *yaml.Node) []string {
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	seen := map[string]bool{}
	dups := make([]string, 0)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		if seen[key] {
			dups = append(dups, key)
			continue
		}
		seen[key] = true
	}
	return dups
}

// parseTemplateYAML 解析模板内容为 yaml.Node（保留注释/锚点/别名）。
func parseTemplateYAML(content string) (*yaml.Node, templateNodeStats, error) {
	var stats templateNodeStats
	if strings.TrimSpace(content) == "" {
		return nil, stats, errors.New("内容为空")
	}
	if len(content) > templateEditorMaxBytes {
		return nil, stats, fmt.Errorf("内容过大（%d 字节，上限 %d 字节）", len(content), templateEditorMaxBytes)
	}
	if !utf8.ValidString(content) {
		return nil, stats, errors.New("内容必须是合法 UTF-8 文本")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return nil, stats, fmt.Errorf("YAML 解析失败：%v", err)
	}
	if len(doc.Content) == 0 {
		return nil, stats, errors.New("YAML 内容为空")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, stats, errors.New("顶层必须是 YAML map（Clash 配置）")
	}
	if err := scanTemplateNode(root, 0, &stats); err != nil {
		return nil, stats, err
	}
	if dups := topLevelDuplicateKeys(root); len(dups) > 0 {
		return nil, stats, fmt.Errorf("顶层键重复：%s", strings.Join(dups, "、"))
	}
	return &doc, stats, nil
}

// encodeTemplateYAML 把 Node 编码为 YAML 文本（2 空格缩进，保留注释/锚点）。
func encodeTemplateYAML(doc *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// templateNodeToAny 把 Node 解码为通用 JSON 值（别名/merge key 会被展开）。
func templateNodeToAny(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// templateSections 区段化输出（前端表单编辑用）。
type templateSections struct {
	Base          map[string]any         `json:"base"`
	DNS           any                    `json:"dns"`
	TUN           any                    `json:"tun"`
	ProxyGroups   any                    `json:"proxy_groups"`
	Rules         any                    `json:"rules"`
	RuleProviders any                    `json:"rule_providers"`
	Summary       templateContentSummary `json:"summary"`

	// rawProxies 顶层 proxies 的原始解码值（系统注入位，只用于校验，不对外输出）。
	rawProxies any
}

var templateSectionTopKeys = map[string]string{
	"dns":            "dns",
	"tun":            "tun",
	"proxy_groups":   "proxy-groups",
	"rules":          "rules",
	"rule_providers": "rule-providers",
}

var templateReservedBaseKeys = map[string]bool{
	"dns": true, "tun": true, "proxy-groups": true, "rules": true, "rule-providers": true, "proxies": true,
}

// sectionsFromNode 把根节点拆成结构化区段。
func sectionsFromNode(root *yaml.Node, stats templateNodeStats) (templateSections, error) {
	out := templateSections{
		Base:          map[string]any{},
		ProxyGroups:   []any{},
		Rules:         []any{},
		RuleProviders: map[string]any{},
		Summary:       stats.summary(root),
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i].Value
		val, err := templateNodeToAny(root.Content[i+1])
		if err != nil {
			return out, fmt.Errorf("解析区段 %s 失败：%v", key, err)
		}
		switch key {
		case "dns":
			out.DNS = val
		case "tun":
			out.TUN = val
		case "proxy-groups":
			out.ProxyGroups = val
		case "rules":
			out.Rules = val
		case "rule-providers":
			out.RuleProviders = val
		case "proxies":
			out.rawProxies = val // 系统注入位：只保存用于校验，不对外输出
		default:
			out.Base[key] = val
		}
	}
	return out, nil
}

// jsonValueToYAML 把 JSON 解码值转成 yaml.Marshal 友好的类型（保留数字精度）。
func jsonValueToYAML(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = jsonValueToYAML(val)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, jsonValueToYAML(val))
		}
		return out
	case nil:
		return nil
	default:
		return v
	}
}

// jsonToYAMLNode 把 JSON 值转成 yaml.Node（用于区段替换）。
func jsonToYAMLNode(raw json.RawMessage) (*yaml.Node, any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, nil, fmt.Errorf("data 不是合法 JSON：%v", err)
	}
	b, err := yaml.Marshal(jsonValueToYAML(v))
	if err != nil {
		return nil, nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 {
		return nil, nil, errors.New("data 为空")
	}
	return doc.Content[0], v, nil
}

// topLevelValueIndex 返回顶层键在 root.Content 中的下标（键节点下标，值节点为 +1）。
func topLevelValueIndex(root *yaml.Node, key string) int {
	if root == nil || root.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// setTopLevelNode 替换/新增/删除顶层键的节点（保留键节点自身的注释）。
func setTopLevelNode(root *yaml.Node, key string, value *yaml.Node, remove bool) {
	idx := topLevelValueIndex(root, key)
	if remove {
		if idx >= 0 {
			root.Content = append(root.Content[:idx], root.Content[idx+2:]...)
		}
		return
	}
	if idx >= 0 {
		root.Content[idx+1] = value
		return
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	root.Content = append(root.Content, keyNode, value)
}

// patchTemplateSection 结构化区段 patch：只替换目标区段，其它区段原样保留。
//
// data 语义：
//   - base：JSON object；逐个顶层键替换/新增；值为 null 表示删除该键；不允许包含保留区段名；
//   - dns / tun：JSON object 或 null（null=删除该区段）；
//   - proxy_groups / rules / rule_providers：JSON 数组/对象整体替换；
//   - proxies：禁止编辑（系统注入位）。
func patchTemplateSection(content, section string, data json.RawMessage) (string, templateSections, []string, []string, error) {
	doc, _, err := parseTemplateYAML(content)
	if err != nil {
		return "", templateSections{}, nil, nil, err
	}
	root := doc.Content[0]
	norm := strings.ToLower(strings.TrimSpace(section))
	norm = strings.ReplaceAll(norm, "-", "_")
	if norm == "proxies" {
		return "", templateSections{}, nil, nil, errors.New("proxies 为系统注入位，不允许通过区段编辑写入用户节点")
	}
	topKey, ok := templateSectionTopKeys[norm]
	if !ok && norm != "base" {
		return "", templateSections{}, nil, nil, fmt.Errorf("不支持的区段：%s", section)
	}

	if norm == "base" {
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		var base map[string]any
		if err := dec.Decode(&base); err != nil {
			return "", templateSections{}, nil, nil, errors.New("base 区段 data 必须是 JSON 对象")
		}
		keys := make([]string, 0, len(base))
		for k := range base {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if templateReservedBaseKeys[k] {
				return "", templateSections{}, nil, nil, fmt.Errorf("base 区段不允许修改保留键：%s", k)
			}
			if base[k] == nil {
				setTopLevelNode(root, k, nil, true)
				continue
			}
			raw, _ := json.Marshal(base[k])
			node, _, err := jsonToYAMLNode(raw)
			if err != nil {
				return "", templateSections{}, nil, nil, err
			}
			setTopLevelNode(root, k, node, false)
		}
	} else {
		node, decoded, err := jsonToYAMLNode(data)
		if err != nil {
			return "", templateSections{}, nil, nil, err
		}
		if decoded == nil {
			setTopLevelNode(root, topKey, nil, true)
		} else {
			switch norm {
			case "proxy_groups", "rules":
				if node.Kind != yaml.SequenceNode {
					return "", templateSections{}, nil, nil, fmt.Errorf("%s 区段必须是 JSON 数组", section)
				}
			case "rule_providers":
				if node.Kind != yaml.MappingNode {
					return "", templateSections{}, nil, nil, fmt.Errorf("%s 区段必须是 JSON 对象", section)
				}
			}
			setTopLevelNode(root, topKey, node, false)
		}
	}

	newContent, err := encodeTemplateYAML(doc)
	if err != nil {
		return "", templateSections{}, nil, nil, fmt.Errorf("重新编码 YAML 失败：%v", err)
	}
	// 编码后重新解析（同时也刷新统计/校验）。
	doc2, stats, err := parseTemplateYAML(newContent)
	if err != nil {
		// 极少见：patch 后语法异常（例如 data 内含特殊结构），按错误返回。
		return "", templateSections{}, nil, nil, fmt.Errorf("patch 后 YAML 非法：%v", err)
	}
	sections, err := sectionsFromNode(doc2.Content[0], stats)
	if err != nil {
		return "", templateSections{}, nil, nil, err
	}
	// 关键：patch 后的摘要必须反映新内容体积（前端用它展示/判断是否被改过）。
	sections.Summary.SizeBytes = len(newContent)
	validation := validateTemplateContent(newContent)
	return newContent, sections, validation.Warnings, validation.Errors, nil
}

// ---------------- 校验 ----------------

type templateValidationResult struct {
	OK       bool                   `json:"ok"`
	Errors   []string               `json:"errors"`
	Warnings []string               `json:"warnings"`
	Summary  templateContentSummary `json:"summary"`
}

var templateProxyGroupTypes = map[string]bool{
	"select": true, "url-test": true, "fallback": true, "load-balance": true, "relay": true,
}

// templateRuleKnownTypes 已知的 Clash/Mihomo 规则类型（用于减少"未知类型"误报）。
// 未列出的类型只给 warning，不阻断发布；这里尽量覆盖 mihomo 文档中的常用类型。
var templateRuleKnownTypes = map[string]bool{
	"MATCH": true, "GEOIP": true, "GEOSITE": true,
	"IP-CIDR": true, "IP-CIDR6": true, "IP-SUFFIX": true,
	"SRC-IP-CIDR": true, "SRC-IP-SUFFIX": true,
	"DOMAIN": true, "DOMAIN-SUFFIX": true, "DOMAIN-KEYWORD": true, "DOMAIN-REGEX": true, "DOMAIN-WILDCARD": true,
	"PROCESS-NAME": true, "PROCESS-PATH": true, "PROCESS-NAME-REGEX": true, "PROCESS-PATH-REGEX": true,
	"RULE-SET": true, "AND": true, "OR": true, "NOT": true, "SUB-RULE": true,
	"DST-PORT": true, "SRC-PORT": true, "IN-PORT": true, "IN-TYPE": true, "IN-NAME": true, "IN-MARK": true,
	"NETWORK": true, "DSCP": true, "IP-ASN": true, "ASN": true, "SRC-IP-ASN": true,
	"SRC-GEOIP": true, "SRC-GEOSITE": true, "UID": true,
	"WIFI-SSID": true, "WIFI-BSSID": true,
}

// templateRuleTrailingOptions 规则末尾的可选参数，不属于目标策略（例如
// `IP-CIDR,1.2.3.0/24,DIRECT,no-resolve` 的最后一个逗号段是 no-resolve）。
var templateRuleTrailingOptions = map[string]bool{
	"no-resolve": true,
}

var templateRuleProviderTypes = map[string]bool{"http": true, "file": true, "inline": true}
var templateRuleProviderBehaviors = map[string]bool{"domain": true, "ipcidr": true, "classical": true}
var templateRuleProviderFormats = map[string]bool{"yaml": true, "text": true, "mrs": true}
var templateTunStacks = map[string]bool{"system": true, "gvisor": true, "mixed": true}

func templateAsString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	default:
		return ""
	}
}

func templateAsBoolPtr(v any) *bool {
	switch x := v.(type) {
	case bool:
		return &x
	default:
		return nil
	}
}

func templateAsNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func templateAsStringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, it := range arr {
		if s, ok := it.(string); ok {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

// validateTemplateContent 模板结构校验（发布前必须 ok）。
func validateTemplateContent(content string) templateValidationResult {
	res := templateValidationResult{Errors: []string{}, Warnings: []string{}}
	doc, stats, err := parseTemplateYAML(content)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}
	root := doc.Content[0]
	res.Summary = stats.summary(root)
	res.Summary.SizeBytes = len(content)
	sections, err := sectionsFromNode(root, stats)
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return res
	}

	groupNames := map[string]bool{}
	// proxies：系统注入位，只允许空或直连占位。
	if proxies, ok := sections.rawProxies.([]any); ok && len(proxies) > 0 {
		for _, item := range proxies {
			m, ok := item.(map[string]any)
			if !ok || templateAsString(m["type"]) != "direct" {
				res.Errors = append(res.Errors, "proxies 是系统注入位：只允许为空或 [{name:直连,type:direct}] 占位，不能写入用户节点")
				break
			}
		}
	}

	// proxy-groups
	if groups, ok := sections.ProxyGroups.([]any); ok {
		for idx, item := range groups {
			m, ok := item.(map[string]any)
			if !ok {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups[%d] 必须是对象", idx))
				continue
			}
			name := templateAsString(m["name"])
			if name == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups[%d] 缺少 name", idx))
			} else if groupNames[name] {
				res.Errors = append(res.Errors, "proxy-groups 名称重复："+name)
			} else {
				groupNames[name] = true
			}
			typ := templateAsString(m["type"])
			if typ == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups[%d]（%s）缺少 type", idx, name))
			} else if !templateProxyGroupTypes[typ] {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups（%s）type=%s 不支持（可用：select/url-test/fallback/load-balance/relay）", name, typ))
			}
			if u := templateAsString(m["url"]); u != "" && !httpURLRe.MatchString(u) {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups（%s）url 需以 http:// 或 https:// 开头", name))
			}
			if filter := templateAsString(m["filter"]); filter != "" {
				// Clash 客户端可能支持负向前瞻等 Go regexp 不支持的语法（如线上 lite 模板
				// `^((?!(回国|中国|China|CN|Mainland)).)*$`）。这里只提示兼容性风险，
				// 不能阻断已有模板的保存/发布。
				if _, err := regexp.Compile(filter); err != nil {
					res.Warnings = append(res.Warnings, fmt.Sprintf(
						"proxy-groups（%s）filter 不是合法 Go 正则（客户端兼容性需确认，可能是负向前瞻等语法）：%v", name, err))
				}
			}
			if iv, ok := templateAsNumber(m["interval"]); ok && iv <= 0 {
				res.Errors = append(res.Errors, fmt.Sprintf("proxy-groups（%s）interval 必须大于 0", name))
			}
		}
	}

	// rule-providers（先校验，rules 引用检查要用）
	providerNames := map[string]bool{}
	switch providers := sections.RuleProviders.(type) {
	case nil:
	case map[string]any:
		names := make([]string, 0, len(providers))
		for name := range providers {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			providerNames[name] = true
			m, ok := providers[name].(map[string]any)
			if !ok {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）必须是对象", name))
				continue
			}
			typ := templateAsString(m["type"])
			if typ == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）缺少 type", name))
			} else if !templateRuleProviderTypes[typ] {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）type=%s 不支持（可用：http/file/inline）", name, typ))
			}
			behavior := templateAsString(m["behavior"])
			if behavior == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）缺少 behavior", name))
			} else if !templateRuleProviderBehaviors[behavior] {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）behavior=%s 不支持（可用：domain/ipcidr/classical）", name, behavior))
			}
			if typ == "http" {
				u := templateAsString(m["url"])
				if u == "" {
					res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）type=http 必须提供 url", name))
				} else if !httpURLRe.MatchString(u) {
					res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）url 需以 http:// 或 https:// 开头", name))
				}
			}
			if format := templateAsString(m["format"]); format != "" && !templateRuleProviderFormats[format] {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）format=%s 不支持（可用：yaml/text/mrs）", name, format))
			}
			if iv, ok := templateAsNumber(m["interval"]); ok && iv <= 0 {
				res.Errors = append(res.Errors, fmt.Sprintf("rule-providers（%s）interval 必须大于 0", name))
			}
		}
	default:
		res.Errors = append(res.Errors, "rule-providers 必须是对象")
	}

	// rules
	if rules, ok := sections.Rules.([]any); ok {
		builtinTargets := map[string]bool{"DIRECT": true, "REJECT": true, "REJECT-DROP": true, "PASS": true, "COMPATIBLE": true}
		for idx, item := range rules {
			rule, ok := item.(string)
			if !ok {
				res.Errors = append(res.Errors, fmt.Sprintf("rules[%d] 必须是字符串", idx))
				continue
			}
			rule = strings.TrimSpace(rule)
			if rule == "" {
				res.Errors = append(res.Errors, fmt.Sprintf("rules[%d] 为空", idx))
				continue
			}
			parts := strings.Split(rule, ",")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			typ := parts[0]
			if typ == "" || typ != strings.ToUpper(typ) {
				res.Errors = append(res.Errors, fmt.Sprintf("rules[%d] 规则类型应大写：%s", idx, rule))
				continue
			}
			// 去掉末尾可选参数（如 no-resolve），它不属于目标策略。
			for len(parts) > 2 && templateRuleTrailingOptions[strings.ToLower(parts[len(parts)-1])] {
				parts = parts[:len(parts)-1]
			}
			if len(parts) < 2 {
				res.Errors = append(res.Errors, fmt.Sprintf("rules[%d] 缺少目标策略：%s", idx, rule))
				continue
			}
			target := parts[len(parts)-1]
			if !templateRuleKnownTypes[typ] {
				res.Warnings = append(res.Warnings, fmt.Sprintf("rules[%d] 未识别的规则类型：%s", idx, typ))
			}
			// 只有 RULE-SET 才按「provider 名」校验引用；AND/OR/NOT 的 payload
			// 内含逗号/括号（形如 ((NETWORK,UDP),(DOMAIN,x))），不能按普通
			// rule-set 解析，否则会误报「((NETWORK」之类的 provider 缺失。
			if typ == "RULE-SET" && len(parts) >= 3 {
				ref := parts[1]
				if ref != "" && !strings.ContainsAny(ref, "()") && !providerNames[ref] {
					res.Warnings = append(res.Warnings, fmt.Sprintf("rules[%d] 引用的 rule-provider 不存在：%s", idx, ref))
				}
			}
			if typ == "MATCH" {
				if target != "DIRECT" && target != "REJECT" && !groupNames[target] {
					res.Warnings = append(res.Warnings, fmt.Sprintf("rules[%d] MATCH 目标策略不存在：%s", idx, target))
				}
			} else if !builtinTargets[target] && !groupNames[target] {
				res.Warnings = append(res.Warnings, fmt.Sprintf("rules[%d] 目标策略不存在：%s", idx, target))
			}
		}
	} else if sections.Rules != nil {
		res.Errors = append(res.Errors, "rules 必须是字符串数组")
	}

	// dns / tun 类型检查
	if dns, ok := sections.DNS.(map[string]any); ok {
		if v, exists := dns["enable"]; exists && templateAsBoolPtr(v) == nil {
			res.Errors = append(res.Errors, "dns.enable 必须是布尔值")
		}
		if v, exists := dns["listen"]; exists && templateAsString(v) == "" && v != "" {
			res.Errors = append(res.Errors, "dns.listen 必须是字符串")
		}
		for _, key := range []string{"nameserver", "fallback", "default-nameserver", "proxy-server-nameserver", "fake-ip-filter", "fallback-filter"} {
			if v, exists := dns[key]; exists {
				if _, isArr := v.([]any); !isArr && key != "fallback-filter" {
					res.Errors = append(res.Errors, "dns."+key+" 必须是数组")
				}
			}
		}
		if v, exists := dns["nameserver-policy"]; exists {
			if _, isMap := v.(map[string]any); !isMap {
				res.Errors = append(res.Errors, "dns.nameserver-policy 必须是对象")
			}
		}
	} else if sections.DNS != nil {
		res.Errors = append(res.Errors, "dns 必须是对象")
	}
	if tun, ok := sections.TUN.(map[string]any); ok {
		if v, exists := tun["enable"]; exists && templateAsBoolPtr(v) == nil {
			res.Errors = append(res.Errors, "tun.enable 必须是布尔值")
		}
		if stack := templateAsString(tun["stack"]); stack != "" && !templateTunStacks[stack] {
			res.Errors = append(res.Errors, "tun.stack 仅支持 system/gvisor/mixed")
		}
		if v, exists := tun["dns-hijack"]; exists {
			if _, isArr := v.([]any); !isArr {
				res.Errors = append(res.Errors, "tun.dns-hijack 必须是数组")
			}
		}
	} else if sections.TUN != nil {
		res.Errors = append(res.Errors, "tun 必须是对象")
	}

	res.OK = len(res.Errors) == 0
	return res
}

// ---------------- 受管 R2 与发布 ----------------

// templateObjectStore 发布用的对象存储抽象（R2 实现；单测可注入 fake）。
type templateObjectStore interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get 返回内容；不存在时 found=false 且 err=nil。
	Get(ctx context.Context, key string) (data []byte, found bool, err error)
}

var newTemplateObjectStore = func(cfg r2Config) templateObjectStore {
	return &r2TemplateObjectStore{client: newR2Client(cfg)}
}

type r2TemplateObjectStore struct {
	client *r2Client
}

func (s *r2TemplateObjectStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	sum := sha256.Sum256(data)
	return s.client.put(ctx, key, bytes.NewReader(data), int64(len(data)), hex.EncodeToString(sum[:]), contentType)
}

func (s *r2TemplateObjectStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	resp, err := s.client.get(ctx, key)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false, fmt.Errorf("R2 读取失败：HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, templateEditorMaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}

// templateManagedHosts 允许发布的受管域名（默认 R2 自定义域 + 配置的 CDN 域）。
func (h *handlers) templateManagedHosts(ctx context.Context) []string {
	hosts := []string{templateManagedR2HostName}
	if base := strings.TrimSpace(h.setting(ctx, settingCDNBaseURL, h.cfg.CDNBaseURL)); base != "" {
		if u, err := url.Parse(base); err == nil && u.Hostname() != "" {
			host := strings.ToLower(u.Hostname())
			if host != templateManagedR2HostName {
				hosts = append(hosts, host)
			}
		}
	}
	return hosts
}

const defaultTemplateBucketBase = "https://" + templateManagedR2HostName

// normalizeTemplateBucketKey 校验模板地址 key：
// 必须位于 rule/configs/ 下、以 .yaml/.yml 结尾；禁止协议/query/fragment/反斜杠与路径穿越。
func normalizeTemplateBucketKey(raw string) (string, error) {
	key := strings.TrimSpace(raw)
	if key == "" {
		return "", errors.New("bucket_key 不能为空")
	}
	if strings.Contains(key, "://") || strings.ContainsAny(key, "\\?#\r\n\t ") {
		return "", errors.New("bucket_key 只能是 rule/configs/*.yaml|yml")
	}
	key = strings.TrimPrefix(key, "/")
	if len(key) > 512 {
		return "", errors.New("bucket_key 过长")
	}
	decoded, err := url.PathUnescape(key)
	if err != nil {
		return "", errors.New("bucket_key 编码无效")
	}
	if strings.Contains(decoded, "..") {
		return "", errors.New("bucket_key 不允许包含 ..")
	}
	if !strings.HasPrefix(decoded, strings.TrimPrefix(templateManagedR2Prefix, "/")) {
		return "", fmt.Errorf("bucket_key 必须以 %s 开头", strings.TrimPrefix(templateManagedR2Prefix, "/"))
	}
	lower := strings.ToLower(decoded)
	if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
		return "", errors.New("bucket_key 必须以 .yaml/.yml 结尾")
	}
	return decoded, nil
}

// templateBucketKeyFromURL 从受管 URL 中解析 key；外部 URL/非法 URL 返回错误。
func templateBucketKeyFromURL(rawURL string, allowedHosts []string) (string, error) {
	return managedTemplateObjectKey(rawURL, allowedHosts)
}

// templateBucketBase 返回模板对象 URL 基址：优先 CDN 基址，未配置时用默认 S3。
func (h *handlers) templateBucketBase(ctx context.Context) string {
	base := strings.TrimRight(h.effectiveCDNBase(ctx), "/")
	if base == "" {
		return defaultTemplateBucketBase
	}
	return base
}

// templateBucketURL 由 key 推导模板对象 URL。
func templateBucketURL(base, key string) string {
	return objectCDNURL(base, key)
}

// managedTemplateObjectKey 校验模板 URL 是否属于受管 R2，并返回对象 key。
func managedTemplateObjectKey(rawURL string, allowedHosts []string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" || !strings.EqualFold(u.Scheme, "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("模板 URL 无效（必须是受管 HTTPS 地址）")
	}
	host := strings.ToLower(u.Hostname())
	allowed := false
	for _, h := range allowedHosts {
		if strings.EqualFold(host, h) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("外部模板只读：仅允许发布到受管 R2（%s），如需编辑请先转为托管模板", strings.Join(allowedHosts, " / "))
	}
	path, err := url.PathUnescape(u.EscapedPath())
	if err != nil || path == "" {
		return "", errors.New("模板 URL 路径编码无效")
	}
	if !strings.HasPrefix(path, templateManagedR2Prefix) {
		return "", fmt.Errorf("模板 URL 路径必须以 %s 开头", templateManagedR2Prefix)
	}
	key := strings.TrimPrefix(path, "/")
	lower := strings.ToLower(key)
	if !strings.HasSuffix(lower, ".yaml") && !strings.HasSuffix(lower, ".yml") {
		return "", errors.New("模板 URL 必须以 .yaml/.yml 结尾")
	}
	if strings.Contains(key, "..") {
		return "", errors.New("模板 URL 路径不合法")
	}
	return key, nil
}

// templateContentRow config_templates 的编辑相关字段。
type templateContentRow struct {
	ID               int64
	Name             string
	URL              string
	Description      string
	DraftContent     string
	PublishedContent string
	DraftSHA         string
	PublishedSHA     string
	DraftUpdatedAt   int64
	DraftUpdatedBy   string
	PublishedAt      int64
	PublishedBy      string
	ContentVersion   int64
}

func (h *handlers) loadTemplateContentRow(ctx context.Context, id int64) (templateContentRow, error) {
	var row templateContentRow
	err := h.db.QueryRowContext(ctx,
		`SELECT id, name, COALESCE(url,''), COALESCE(description,''),
		        COALESCE(draft_content,''), COALESCE(published_content,''),
		        COALESCE(draft_sha256,''), COALESCE(published_sha256,''),
		        COALESCE(draft_updated_at,0), COALESCE(draft_updated_by,''),
		        COALESCE(published_at,0), COALESCE(published_by,''),
		        COALESCE(content_version,0)
		   FROM config_templates WHERE id = ? LIMIT 1`, id).
		Scan(&row.ID, &row.Name, &row.URL, &row.Description, &row.DraftContent, &row.PublishedContent,
			&row.DraftSHA, &row.PublishedSHA, &row.DraftUpdatedAt, &row.DraftUpdatedBy,
			&row.PublishedAt, &row.PublishedBy, &row.ContentVersion)
	return row, err
}

// ---------------- 模板 URL 拉取（带 5 分钟缓存） ----------------

type cachedTemplateText struct {
	content string
	at      time.Time
}

var templateURLCache = struct {
	sync.Mutex
	m map[string]cachedTemplateText
}{m: map[string]cachedTemplateText{}}

// templateHTTPFetch 网络拉取注入点（单测替换，避免真实联网）。
var templateHTTPFetch = func(ctx context.Context, rawURL string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", templateURLUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, templateURLMaxBytes))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(body)) == "" {
		return "", errors.New("内容为空")
	}
	return string(body), nil
}

// fetchTemplateURLText 带 5 分钟内存缓存的模板 URL 读取（运行时/编辑器共用）。
func fetchTemplateURLText(ctx context.Context, rawURL string) (string, error) {
	now := time.Now()
	templateURLCache.Lock()
	if c, ok := templateURLCache.m[rawURL]; ok && now.Sub(c.at) < templateURLContentTTL {
		templateURLCache.Unlock()
		return c.content, nil
	}
	templateURLCache.Unlock()
	content, err := templateHTTPFetch(ctx, rawURL)
	if err != nil {
		return "", err
	}
	templateURLCache.Lock()
	templateURLCache.m[rawURL] = cachedTemplateText{content: content, at: now}
	templateURLCache.Unlock()
	return content, nil
}

func clearTemplateURLCache() {
	templateURLCache.Lock()
	templateURLCache.m = map[string]cachedTemplateText{}
	templateURLCache.Unlock()
}

// ---------------- 版本 ----------------

func (h *handlers) insertTemplateVersion(ctx context.Context, templateID, version int64, content, sha, action, actor string) error {
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO config_template_versions (template_id, version, content, sha256, action, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		templateID, version, content, sha, action, actor, nowMillis())
	return err
}

// sha256String 计算字符串 SHA-256（hex）。
func sha256String(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ---------------- 有效内容 / 运行时模板解析 ----------------

var errTemplateNotFound = errors.New("模板不存在")

// effectiveTemplateContent 返回编辑器/预览使用的内容：
// 存储桶 URL 是唯一内容源；附件管理里的修改会直接反映到模板编辑器/订阅。
// 只有 URL 暂时不可用时才回退草稿/已发布 DB 副本，避免完全无法编辑。
func (h *handlers) effectiveTemplateContent(ctx context.Context, row templateContentRow) (content, source string, err error) {
	content, urlErr := fetchTemplateURLText(ctx, row.URL)
	if urlErr == nil {
		return content, "url", nil
	}
	if strings.TrimSpace(row.DraftContent) != "" {
		return row.DraftContent, "draft", nil
	}
	if strings.TrimSpace(row.PublishedContent) != "" {
		return row.PublishedContent, "published", nil
	}
	return "", "url", urlErr
}

// clashTemplateContent 运行时解析出的模板内容与来源。
type clashTemplateContent struct {
	Content string
	Source  string // db | url
	URL     string // 用于兼容的 X-Template-Used
}

// resolveClashTemplateContent 运行时模板解析：始终读取模板 URL 指向的存储桶文件。
// 已发布 DB 副本只用于编辑器回退/版本历史，不再参与用户订阅内容，避免附件管理修改后订阅仍旧。
func (h *handlers) resolveClashTemplateContent(ctx context.Context, template string) (clashTemplateContent, error) {
	template = strings.TrimSpace(template)
	if strings.HasPrefix(template, "http://") || strings.HasPrefix(template, "https://") {
		content, err := fetchTemplateURLText(ctx, template)
		if err != nil {
			return clashTemplateContent{}, fmt.Errorf("模板载入失败: %v", err)
		}
		return clashTemplateContent{Content: content, Source: "url", URL: template}, nil
	}
	var rawURL string
	err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(url,'') FROM config_templates WHERE name = ? LIMIT 1`, template).
		Scan(&rawURL)
	if errors.Is(err, sql.ErrNoRows) {
		return clashTemplateContent{}, fmt.Errorf("%w：%s", errTemplateNotFound, template)
	}
	if err != nil {
		return clashTemplateContent{}, errors.New("查询模板失败")
	}
	content, err := fetchTemplateURLText(ctx, rawURL)
	if err != nil {
		return clashTemplateContent{}, fmt.Errorf("模板载入失败: %v", err)
	}
	return clashTemplateContent{Content: content, Source: "url", URL: rawURL}, nil
}

// ---------------- HTTP handlers ----------------

// GET /api/templates/manage/{id}/content
func (h *handlers) templateContentGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	row, err := h.loadTemplateContentRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	content, source, cerr := h.effectiveTemplateContent(r.Context(), row)
	if cerr != nil {
		writeError(w, http.StatusBadGateway, "从模板 URL 读取失败: "+cerr.Error())
		return
	}
	summary := templateContentSummary{}
	if doc, stats, perr := parseTemplateYAML(content); perr == nil {
		summary = stats.summary(doc.Content[0])
		summary.SizeBytes = len(content)
	}
	managed, managedErr, bucketKey := false, "", ""
	if key, kerr := templateBucketKeyFromURL(row.URL, h.templateManagedHosts(r.Context())); kerr != nil {
		managedErr = kerr.Error()
	} else {
		managed = true
		bucketKey = key
	}
	publishable := managed && h.effectiveR2(r.Context()).Ready()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"id":                row.ID,
			"name":              row.Name,
			"url":               row.URL,
			"bucket_key":        bucketKey,
			"description":       row.Description,
			"draft_content":     row.DraftContent,
			"published_content": row.PublishedContent,
			"draft_sha256":      row.DraftSHA,
			"published_sha256":  row.PublishedSHA,
			"draft_updated_at":  row.DraftUpdatedAt,
			"draft_updated_by":  row.DraftUpdatedBy,
			"published_at":      row.PublishedAt,
			"published_by":      row.PublishedBy,
			"content_version":   row.ContentVersion,
			"source":            source,
			"content":           content,
			"summary":           summary,
			"has_draft":         strings.TrimSpace(row.DraftContent) != "",
			"dirty":             strings.TrimSpace(row.DraftContent) != "" && row.DraftSHA != row.PublishedSHA,
			"managed":           managed,
			"managed_error":     managedErr,
			"publishable":       publishable,
		},
	})
}

// templateEditorBody 编辑器相关接口的通用请求体（content 为空时用有效内容）。
type templateEditorBody struct {
	Content     string          `json:"content"`
	BaseSHA256  string          `json:"base_sha256"`
	Section     string          `json:"section"`
	Data        json.RawMessage `json:"data"`
	UUID        string          `json:"uuid"`
	ContentPref string          `json:"-"`
}

func (h *handlers) templatePayloadContent(ctx context.Context, id int64, raw string) (string, templateContentRow, error) {
	if strings.TrimSpace(raw) != "" {
		return raw, templateContentRow{ID: id}, nil
	}
	row, err := h.loadTemplateContentRow(ctx, id)
	if err != nil {
		return "", row, err
	}
	content, _, err := h.effectiveTemplateContent(ctx, row)
	return content, row, err
}

// POST /api/templates/manage/{id}/validate
func (h *handlers) templateValidate(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	content, _, err := h.templatePayloadContent(r.Context(), id, body.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "模板不存在")
			return
		}
		writeError(w, http.StatusBadRequest, "没有可校验的内容（请提供 content 或先导入 URL）")
		return
	}
	result := validateTemplateContent(content)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}

// POST /api/templates/manage/{id}/parse
func (h *handlers) templateParse(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	content, _, err := h.templatePayloadContent(r.Context(), id, body.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "模板不存在")
			return
		}
		writeError(w, http.StatusBadRequest, "没有可解析的内容（请提供 content 或先导入 URL）")
		return
	}
	doc, stats, perr := parseTemplateYAML(content)
	if perr != nil {
		writeError(w, http.StatusBadRequest, perr.Error())
		return
	}
	sections, serr := sectionsFromNode(doc.Content[0], stats)
	if serr != nil {
		writeError(w, http.StatusBadRequest, serr.Error())
		return
	}
	sections.Summary.SizeBytes = len(content)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"sha256":  sha256String(content),
			"summary": sections.Summary,
			"sections": map[string]any{
				"base":           sections.Base,
				"dns":            sections.DNS,
				"tun":            sections.TUN,
				"proxy_groups":   sections.ProxyGroups,
				"rules":          sections.Rules,
				"rule_providers": sections.RuleProviders,
			},
		},
	})
}

// POST /api/templates/manage/{id}/patch-section
func (h *handlers) templatePatchSection(w http.ResponseWriter, r *http.Request) {
	_, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "缺少 content")
		return
	}
	if strings.TrimSpace(body.Section) == "" {
		writeError(w, http.StatusBadRequest, "缺少 section")
		return
	}
	if len(body.Data) == 0 || string(body.Data) == "null" {
		writeError(w, http.StatusBadRequest, "缺少 data")
		return
	}
	newContent, sections, warnings, verrs, err := patchTemplateSection(body.Content, body.Section, body.Data)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"content":  newContent,
			"sha256":   sha256String(newContent),
			"summary":  sections.Summary,
			"sections": sections,
			"warnings": warnings,
			"errors":   verrs,
		},
	})
}

// saveTemplateDraft 写草稿并记一条版本（内容未变化时按 action 决定是否仍记录）。
func (h *handlers) saveTemplateDraft(ctx context.Context, row templateContentRow, content, actor, action string) (string, int64, error) {
	sha := sha256String(content)
	now := nowMillis()
	if action == "" {
		action = "save_draft"
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE config_templates
		    SET draft_content = ?, draft_sha256 = ?, draft_updated_at = ?, draft_updated_by = ?, updated_at = ?
		  WHERE id = ?`,
		content, sha, now, actor, now, row.ID); err != nil {
		return "", 0, err
	}
	if row.DraftSHA != sha || action == "restore" || action == "import_url" {
		_ = h.insertTemplateVersion(ctx, row.ID, row.ContentVersion, content, sha, action, actor)
	}
	return sha, now, nil
}

// PUT /api/templates/manage/{id}/content —— 保存草稿（base_sha256 乐观锁）
func (h *handlers) templateContentSave(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		writeError(w, http.StatusBadRequest, "内容不能为空")
		return
	}
	if _, _, err := parseTemplateYAML(body.Content); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	validation := validateTemplateContent(body.Content)
	if !validation.OK {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "模板校验未通过", "errors": validation.Errors, "warnings": validation.Warnings,
		})
		return
	}
	row, err := h.loadTemplateContentRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	base := strings.TrimSpace(body.BaseSHA256)
	if base != "" {
		current := row.DraftSHA
		if current == "" {
			current = row.PublishedSHA
		}
		if base != current {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "模板内容已被他人修改，请刷新后重试", "current_sha256": current,
			})
			return
		}
	}

	key, err := templateBucketKeyFromURL(row.URL, h.templateManagedHosts(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, "该模板不是受管存储桶模板，无法直接写存储桶："+err.Error())
		return
	}
	r2cfg := h.effectiveR2(r.Context())
	if !r2cfg.Ready() {
		writeError(w, http.StatusBadRequest, "R2 未配置，无法直接保存模板到存储桶")
		return
	}
	store := newTemplateObjectStore(r2cfg)
	if err := store.Put(r.Context(), key, []byte(body.Content), "text/yaml; charset=utf-8"); err != nil {
		writeError(w, http.StatusBadGateway, "写入存储桶失败："+err.Error())
		return
	}

	sha := sha256String(body.Content)
	now := nowMillis()
	actor := currentActor(r)
	newVersion := row.ContentVersion + 1
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "同步模板数据库失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE config_templates
		    SET draft_content = ?, draft_sha256 = ?, draft_updated_at = ?, draft_updated_by = ?,
		        published_content = ?, published_sha256 = ?, published_at = ?, published_by = ?,
		        content_version = ?, updated_at = ?
		  WHERE id = ?`,
		body.Content, sha, now, actor, body.Content, sha, now, actor, newVersion, now, id); err != nil {
		writeError(w, http.StatusInternalServerError, "同步模板数据库失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO config_template_versions (template_id, version, content, sha256, action, actor, created_at)
		 VALUES (?, ?, ?, ?, 'direct_save', ?, ?)`,
		id, newVersion, body.Content, sha, actor, now); err != nil {
		writeError(w, http.StatusInternalServerError, "记录模板版本失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "同步模板数据库失败")
		return
	}
	clearTemplateURLCache()
	h.recordNodeLog(r.Context(), 0, 0, "template-save-bucket", "success", 0,
		fmt.Sprintf("模板 %d（%s）直写存储桶 key=%s version=%d sha=%s", id, row.Name, key, newVersion, sha), "", actor)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"source": "bucket", "sha256": sha, "url": row.URL, "bucket_key": key,
			"version": newVersion, "content_version": newVersion,
			"draft_updated_at": now, "warnings": validation.Warnings,
		},
	})
}

// templatePublishResult 发布结果。
type templatePublishResult struct {
	URL      string   `json:"url"`
	Version  int64    `json:"version"`
	SHA256   string   `json:"sha256"`
	Warnings []string `json:"warnings"`
}

// POST /api/templates/manage/{id}/publish
func (h *handlers) templatePublish(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	row, err := h.loadTemplateContentRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	content := body.Content
	if strings.TrimSpace(content) == "" {
		content = row.DraftContent
	}
	if strings.TrimSpace(content) == "" {
		content = row.PublishedContent
	}
	if strings.TrimSpace(content) == "" {
		writeError(w, http.StatusBadRequest, "没有可发布的内容（请先保存草稿或从 URL 导入）")
		return
	}
	validation := validateTemplateContent(content)
	if !validation.OK {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "模板校验未通过", "errors": validation.Errors, "warnings": validation.Warnings,
		})
		return
	}
	key, err := managedTemplateObjectKey(row.URL, h.templateManagedHosts(r.Context()))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	r2cfg := h.effectiveR2(r.Context())
	if !r2cfg.Ready() {
		writeError(w, http.StatusBadRequest, "R2 未配置，无法发布到托管模板")
		return
	}
	store := newTemplateObjectStore(r2cfg)
	warnings := append([]string{}, validation.Warnings...)
	now := nowMillis()
	// 备份旧对象（失败不阻断，只告警）
	if old, found, gerr := store.Get(r.Context(), key); gerr != nil {
		warnings = append(warnings, "读取旧模板对象失败："+gerr.Error())
	} else if found && len(old) > 0 {
		base := key
		if idx := strings.LastIndex(key, "/"); idx >= 0 {
			base = key[idx+1:]
		}
		backupKey := "rule/configs/.versions/" + base + "." + itoa64(now) + ".yaml"
		if perr := store.Put(r.Context(), backupKey, old, "text/yaml; charset=utf-8"); perr != nil {
			warnings = append(warnings, "备份旧模板对象失败："+perr.Error())
		}
	}
	if err := store.Put(r.Context(), key, []byte(content), "text/yaml; charset=utf-8"); err != nil {
		writeError(w, http.StatusBadGateway, "写入 R2 失败："+err.Error())
		return
	}
	sha := sha256String(content)
	newVersion := row.ContentVersion + 1
	actor := currentActor(r)
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "发布失败")
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(),
		`UPDATE config_templates
		    SET published_content = ?, published_sha256 = ?, published_at = ?, published_by = ?,
		        content_version = ?, draft_content = ?, draft_sha256 = ?, draft_updated_at = ?, draft_updated_by = ?,
		        updated_at = ?
		  WHERE id = ?`,
		content, sha, now, actor, newVersion, content, sha, now, actor, now, id); err != nil {
		writeError(w, http.StatusInternalServerError, "发布失败")
		return
	}
	if _, err := tx.ExecContext(r.Context(),
		`INSERT INTO config_template_versions (template_id, version, content, sha256, action, actor, created_at)
		 VALUES (?, ?, ?, ?, 'publish', ?, ?)`,
		id, newVersion, content, sha, actor, now); err != nil {
		writeError(w, http.StatusInternalServerError, "发布失败")
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "发布失败")
		return
	}
	clearTemplateURLCache()
	h.recordNodeLog(r.Context(), 0, 0, "template-publish", "success", 0,
		fmt.Sprintf("模板 %d（%s）发布版本 %d，sha=%s", id, row.Name, newVersion, sha), "", actor)
	h.log.Info("模板已发布", "template_id", id, "name", row.Name, "version", newVersion, "sha256", sha, "actor", actor)
	result := templatePublishResult{URL: row.URL, Version: newVersion, SHA256: sha, Warnings: warnings}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": result})
}

// POST /api/templates/manage/{id}/import-url
func (h *handlers) templateImportURL(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	row, err := h.loadTemplateContentRow(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询模板失败")
		return
	}
	content, err := templateHTTPFetch(r.Context(), row.URL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "从模板 URL 读取失败: "+err.Error())
		return
	}
	doc, stats, perr := parseTemplateYAML(content)
	if perr != nil {
		writeError(w, http.StatusBadRequest, "URL 内容不是合法的 Clash 模板："+perr.Error())
		return
	}
	sections, serr := sectionsFromNode(doc.Content[0], stats)
	if serr != nil {
		writeError(w, http.StatusBadRequest, serr.Error())
		return
	}
	sections.Summary.SizeBytes = len(content)
	sha, updatedAt, err := h.saveTemplateDraft(r.Context(), row, content, currentActor(r), "import_url")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存草稿失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"content": content, "sha256": sha, "source": "url", "draft_updated_at": updatedAt,
			"summary": sections.Summary,
		},
	})
}

// templatePreviewLinks 预览用的订阅链接来源（单测可注入）。
var templatePreviewLinks = func(h *handlers, ctx context.Context, uuid string) ([]string, extSummary, error) {
	return h.subscriptionLines(ctx, uuid)
}

// adminBoundUUID 返回管理员账号邮箱所绑定的 UUID（预览默认值）。
func (h *handlers) adminBoundUUID(ctx context.Context, username string) string {
	if strings.TrimSpace(username) == "" {
		return ""
	}
	var uuid string
	_ = h.db.QueryRowContext(ctx,
		`SELECT ui.uuid FROM uuid_info ui JOIN admin_users au ON LOWER(ui.email) = LOWER(au.email)
		  WHERE au.username = ? AND COALESCE(au.email,'') <> '' ORDER BY ui.id ASC LIMIT 1`, username).Scan(&uuid)
	return strings.TrimSpace(uuid)
}

// POST /api/templates/manage/{id}/preview —— 用草稿/给定内容注入真实 proxies（不写访问日志）
func (h *handlers) templatePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	var body templateEditorBody
	if err := decodeJSON(w, r, &body); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	content, row, err := h.templatePayloadContent(r.Context(), id, body.Content)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "模板不存在")
			return
		}
		writeError(w, http.StatusBadRequest, "没有可预览的内容（请提供 content 或先导入 URL）")
		return
	}
	uuid := strings.TrimSpace(body.UUID)
	if uuid == "" {
		if claims, ok := claimsFrom(r); ok {
			uuid = h.adminBoundUUID(r.Context(), claims.Sub)
		}
	}
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "请提供 uuid 用于预览（或先给管理员账号绑定邮箱/UUID）")
		return
	}
	links, ext, lerr := templatePreviewLinks(h, r.Context(), uuid)
	if lerr != nil {
		writeError(w, http.StatusInternalServerError, "读取订阅配置失败")
		return
	}
	proxies, skippedBy, protocols, reasons := h.buildClashProxies(r.Context(), links)
	if len(proxies) == 0 {
		writeError(w, http.StatusBadRequest, "该 UUID 没有可用的代理节点")
		return
	}
	yamlText := injectClashProxies(content, proxies)
	validation := validateTemplateContent(content)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"yaml":      yamlText,
			"proxies":   len(proxies),
			"total":     len(links),
			"skipped":   skippedBy,
			"protocols": protocols,
			"reasons":   reasons,
			"warnings":  validation.Warnings,
			"external": map[string]any{
				"sources": ext.Sources, "added": ext.Added, "errors": ext.Errors, "unsupported": ext.Unsupported,
			},
			"template": row.Name,
		},
	})
}

// GET /api/templates/manage/{id}/versions
func (h *handlers) templateVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT id, version, action, COALESCE(actor,'') AS actor, sha256,
		        CHAR_LENGTH(content) AS size, CHAR_LENGTH(content) AS content_len, created_at
		   FROM config_template_versions WHERE template_id = ? ORDER BY id DESC LIMIT 100`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询版本失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

// GET /api/templates/manage/{id}/versions/{vid}
func (h *handlers) templateVersionGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	vid, err := strconv.ParseInt(pathParam(r, "vid"), 10, 64)
	if err != nil || vid <= 0 {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	var content, sha, action, actor string
	var version, createdAt int64
	err = h.db.QueryRowContext(r.Context(),
		`SELECT version, content, sha256, action, COALESCE(actor,''), created_at
		   FROM config_template_versions WHERE id = ? AND template_id = ? LIMIT 1`, vid, id).
		Scan(&version, &content, &sha, &action, &actor, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询版本失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"id": vid, "template_id": id, "version": version, "content": content,
			"sha256": sha, "action": action, "actor": actor, "created_at": createdAt,
		},
	})
}

// POST /api/templates/manage/{id}/versions/{vid}/restore —— 恢复为草稿
func (h *handlers) templateVersionRestore(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	vid, err := strconv.ParseInt(pathParam(r, "vid"), 10, 64)
	if err != nil || vid <= 0 {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	var content string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT content FROM config_template_versions WHERE id = ? AND template_id = ? LIMIT 1`, vid, id).
		Scan(&content); err != nil {
		writeError(w, http.StatusNotFound, "版本不存在")
		return
	}
	if _, _, perr := parseTemplateYAML(content); perr != nil {
		writeError(w, http.StatusBadRequest, "该版本内容不是合法 YAML："+perr.Error())
		return
	}
	row, err := h.loadTemplateContentRow(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "模板不存在")
		return
	}
	sha, updatedAt, err := h.saveTemplateDraft(r.Context(), row, content, currentActor(r), "restore")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "恢复版本失败")
		return
	}
	validation := validateTemplateContent(content)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data": map[string]any{
			"content": content, "sha256": sha, "content_version": row.ContentVersion,
			"draft_updated_at": updatedAt, "warnings": validation.Warnings, "restored_version": vid,
		},
	})
}
