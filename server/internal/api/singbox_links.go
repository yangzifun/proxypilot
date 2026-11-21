package api

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// singboxCDNEntry 是单节点多 CDN 的一个条目（df 0059）。
//
// 兼容说明：
//   - 旧数据只有 node.cdn_domain（单域名），effectiveCDNEntries 会把它转成
//     一个 name="" 的 legacy 条目；
//   - 新数据存 singbox_nodes.cdn_entries（JSON 数组），同时继续维护
//     cdn_domain 列为第一个启用条目的域名（兼容旧前端/旧订阅逻辑）。
type singboxCDNEntry struct {
	Name    string `json:"name"`
	Domain  string `json:"domain"`
	Primary bool   `json:"primary"`
	// Enabled 缺省（nil / 字段不存在）视为启用；显式 false 表示停用。
	// 指针用于区分“旧数据/旧请求没传”和“明确停用”。
	Enabled     *bool  `json:"enabled,omitempty"`
	PreferGroup string `json:"prefer_group"`
}

func cdnBoolPtr(v bool) *bool { b := v; return &b }

// cdnEntryEnabled 缺省启用；只有显式 false 才停用（兼容旧数据/旧请求）。
func cdnEntryEnabled(e singboxCDNEntry) bool { return e.Enabled == nil || *e.Enabled }

const (
	singboxMaxCDNEntries = 8
	singboxMaxCDNNameLen = 24
	singboxMaxCDNLinks   = 32
)

type singboxNode struct {
	Name       string            `json:"name"`
	Domain     string            `json:"domain"`
	CDNDomain  string            `json:"cdn_domain"`
	CDNEntries []singboxCDNEntry `json:"cdn_entries,omitempty"`
}

func (t *singboxTemplate) engineOn(key string) bool {
	switch key {
	case "masque":
		return t.EnableMasque == 1
	}
	if t.EnableSingbox != nil {
		return *t.EnableSingbox == 1
	}
	return true
}

// x25519PublicKeyFromPrivate 由 REALITY 私钥推导公钥（base64url，无填充），与 Worker 一致。
func x25519PublicKeyFromPrivate(privateB64 string) string {
	raw, ok := decodeAnyBase64(privateB64)
	if !ok || len(raw) != 32 {
		return ""
	}
	priv, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes())
}

func (t *singboxTemplate) realityPublicKey() string {
	if explicit := strings.TrimSpace(t.RealityPublicKey); explicit != "" {
		return explicit
	}
	priv := strings.TrimSpace(t.RealityPrivateKey)
	if priv == "" {
		return ""
	}
	return x25519PublicKeyFromPrivate(priv)
}

// normalizeCDNEntryDomain 在 normalizeDomain 基础上做小写化，并显式拒绝 IP。
// normalizeDomain 会剥掉协议/路径（兼容旧 cdn_domain 行为），但主机名规则会放行
// 1.2.3.4 这类 IPv4，多 CDN 的域名契约不允许 IP，所以这里补一道校验。
func normalizeCDNEntryDomain(raw string) string {
	d := strings.ToLower(strings.TrimSpace(normalizeDomain(raw)))
	if d == "" {
		return ""
	}
	if net.ParseIP(d) != nil {
		return ""
	}
	return d
}

// finalizeCDNEntries 仅补 enabled 缺省值。primary 仅作为旧 JSON 兼容字段保留，
// 不参与排序、唯一性校验或 fallback；条目顺序保持原顺序。
func finalizeCDNEntries(entries []singboxCDNEntry) []singboxCDNEntry {
	if len(entries) == 0 {
		return nil
	}
	for i := range entries {
		if entries[i].Enabled == nil {
			entries[i].Enabled = cdnBoolPtr(true) // 旧数据/旧请求缺省启用
		}
	}
	return entries
}

// normalizeStoredCDNEntries 对数据库中已存/旧数据做宽松解析：坏条目丢弃，
// 域名去重、小写；enabled 缺省 true；保持原条目顺序，忽略 primary 语义。
// 用于响应与链接生成，不返回错误。
func normalizeStoredCDNEntries(entries []singboxCDNEntry) []singboxCDNEntry {
	out := make([]singboxCDNEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		domain := normalizeCDNEntryDomain(e.Domain)
		if domain == "" || seen[domain] {
			continue
		}
		seen[domain] = true
		name := normalizeCDNEntryName(e.Name)
		if utf8.RuneCountInString(name) > singboxMaxCDNNameLen {
			name = string([]rune(name)[:singboxMaxCDNNameLen])
		}
		group := strings.TrimSpace(e.PreferGroup)
		if len(group) > 64 {
			group = group[:64]
		}
		enabled := e.Enabled
		if enabled == nil {
			enabled = cdnBoolPtr(true) // 旧数据/旧请求缺省启用
		}
		// Primary 原样保留（旧 JSON 兼容），但不再影响任何逻辑。
		out = append(out, singboxCDNEntry{
			Name: name, Domain: domain, Primary: e.Primary, Enabled: enabled, PreferGroup: group,
		})
		if len(out) == singboxMaxCDNEntries {
			break
		}
	}
	return finalizeCDNEntries(out)
}

// enabledCDNEntries 只保留启用的条目（链接生成/兼容 cdn_domain 判断使用）。
func enabledCDNEntries(entries []singboxCDNEntry) []singboxCDNEntry {
	out := make([]singboxCDNEntry, 0, len(entries))
	for _, e := range entries {
		if cdnEntryEnabled(e) {
			out = append(out, e)
		}
	}
	return out
}

// parseStoredCDNEntries 解析 DB 里的 cdn_entries JSON；为空/损坏时回退旧 cdn_domain。
func parseStoredCDNEntries(raw, legacyDomain string) []singboxCDNEntry {
	raw = strings.TrimSpace(raw)
	if raw != "" && raw != "[]" && !strings.EqualFold(raw, "null") {
		var arr []singboxCDNEntry
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			if normalized := normalizeStoredCDNEntries(arr); len(normalized) > 0 {
				return normalized
			}
		}
	}
	legacy := normalizeCDNEntryDomain(legacyDomain)
	if legacy == "" {
		return nil
	}
	return []singboxCDNEntry{{Domain: legacy, Primary: true, Enabled: cdnBoolPtr(true)}}
}

// parseCDNEntriesValue 兼容内存中的多种表示（string JSON / []singboxCDNEntry / []any），
// 主要给 inputToNode/测试/预览使用；DB 行仍是 JSON 字符串。
func parseCDNEntriesValue(v any) []singboxCDNEntry {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return parseStoredCDNEntries(x, "")
	case []singboxCDNEntry:
		return normalizeStoredCDNEntries(x)
	case []any:
		b, err := json.Marshal(x)
		if err != nil {
			return nil
		}
		var arr []singboxCDNEntry
		if json.Unmarshal(b, &arr) != nil {
			return nil
		}
		return normalizeStoredCDNEntries(arr)
	}
	return nil
}

// effectiveCDNEntries 返回用于链接生成的条目：显式 entries 优先，否则回退旧单域名。
func effectiveCDNEntries(node singboxNode) []singboxCDNEntry {
	if len(node.CDNEntries) > 0 {
		if normalized := normalizeStoredCDNEntries(node.CDNEntries); len(normalized) > 0 {
			return normalized
		}
	}
	return parseStoredCDNEntries("", node.CDNDomain)
}

func cdnEntriesJSON(entries []singboxCDNEntry) string {
	entries = normalizeStoredCDNEntries(entries)
	if len(entries) == 0 {
		return "[]"
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// cdnDomainsOf 是 cdn_entries 的兼容投影：只包含启用条目的域名，
// 与 0059 前 cdn_domains 被用于生成链接的语义保持一致（停用 CDN 不外泄给旧订阅）。
func cdnDomainsOf(entries []singboxCDNEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !cdnEntryEnabled(e) {
			continue
		}
		out = append(out, e.Domain)
	}
	return out
}

// cdnJSONFields 统一列表/详情/预览响应里的三个兼容字段（数组不为 null）。
func cdnJSONFields(entries []singboxCDNEntry) map[string]any {
	entries = normalizeStoredCDNEntries(entries)
	if entries == nil {
		entries = []singboxCDNEntry{}
	}
	domains := cdnDomainsOf(entries)
	if domains == nil {
		domains = []string{}
	}
	return map[string]any{
		"cdn_entries": entries,
		"cdn_domains": domains,
		"cdn_domain":  cdnPrimaryDomain(entries),
	}
}

// cdnPrimaryDomain 仅为兼容旧 cdn_domain 列：取第一个启用条目的域名。
// 注意：CDN 已不分主/备，这里不再看 primary 字段（primary 仅作旧 JSON 解析保留）。
func cdnPrimaryDomain(entries []singboxCDNEntry) string {
	for _, e := range entries {
		if cdnEntryEnabled(e) {
			return e.Domain
		}
	}
	return ""
}

// normalizeCDNEntryName 统一旧名称：CF → Cloudflare；其它名称保持原样。
func normalizeCDNEntryName(raw string) string {
	name := strings.TrimSpace(raw)
	if strings.EqualFold(name, "CF") {
		return "Cloudflare"
	}
	return name
}

func cdnEntryLabel(e singboxCDNEntry) string {
	name := normalizeCDNEntryName(e.Name)
	if name == "" {
		return "CDN"
	}
	return name
}

// normalizeCDNInput 解析请求中的 cdn_entries / cdn_domains / cdn_domain 三种输入。
// 返回规范化后的条目和错误文案（错误文案带条目序号）。
func normalizeCDNInput(body map[string]any) ([]singboxCDNEntry, string) {
	var (
		entries     []singboxCDNEntry
		requireName bool
	)

	if raw, ok := body["cdn_entries"]; ok && raw != nil {
		requireName = true
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				if err := json.Unmarshal([]byte(v), &entries); err != nil {
					return nil, "cdn_entries 格式无效"
				}
			}
		case []any:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, "cdn_entries 格式无效"
			}
			if err := json.Unmarshal(b, &entries); err != nil {
				return nil, "cdn_entries 格式无效"
			}
		case []map[string]any:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, "cdn_entries 格式无效"
			}
			if err := json.Unmarshal(b, &entries); err != nil {
				return nil, "cdn_entries 格式无效"
			}
		case []singboxCDNEntry:
			entries = v
		default:
			return nil, "cdn_entries 格式无效"
		}
	} else if raw, ok := body["cdn_domains"]; ok && raw != nil {
		var domains []string
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				if err := json.Unmarshal([]byte(v), &domains); err != nil {
					return nil, "cdn_domains 格式无效"
				}
			}
		case []any:
			for _, item := range v {
				domains = append(domains, jsonStr(item))
			}
		case []string:
			domains = v
		default:
			return nil, "cdn_domains 格式无效"
		}
		for _, d := range domains {
			entries = append(entries, singboxCDNEntry{Domain: d})
		}
	} else {
		legacy := strings.TrimSpace(mapStr(body, "cdn_domain", ""))
		if legacy != "" {
			entries = []singboxCDNEntry{{Domain: legacy}}
		}
	}

	if len(entries) > singboxMaxCDNEntries {
		return nil, "CDN 条目最多 " + strconv.Itoa(singboxMaxCDNEntries) + " 个"
	}
	if len(entries) == 0 {
		return nil, ""
	}

	seen := map[string]bool{}
	for i := range entries {
		idx := i + 1
		name := normalizeCDNEntryName(entries[i].Name)
		if requireName {
			if name == "" {
				return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 名称不能为空"
			}
			if utf8.RuneCountInString(name) > singboxMaxCDNNameLen {
				return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 名称不能超过 " + strconv.Itoa(singboxMaxCDNNameLen) + " 个字符"
			}
			for _, r := range name {
				if unicode.IsControl(r) || r == '\n' || r == '\r' {
					return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 名称包含非法字符"
				}
			}
		} else {
			name = ""
		}
		rawDomain := strings.TrimSpace(entries[i].Domain)
		domain := normalizeCDNEntryDomain(rawDomain)
		if domain == "" {
			return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 域名无效"
		}
		// 新条目要求“只填域名”：normalizeDomain 会剥协议/路径，但 cdn_entries 是
		// 新契约，显式拒绝带协议/路径/端口的输入（旧 cdn_domain 仍保持原兼容行为）。
		if requireName && strings.ToLower(rawDomain) != domain {
			return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 域名无效（只填域名，不要带协议、路径或端口）"
		}
		if seen[domain] {
			return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 域名重复"
		}
		seen[domain] = true
		group := strings.TrimSpace(entries[i].PreferGroup)
		if group != "" {
			if len(group) > 64 {
				return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 优选分组过长（最多 64 字符）"
			}
			for _, r := range group {
				if unicode.IsControl(r) || unicode.IsSpace(r) {
					return nil, "第 " + strconv.Itoa(idx) + " 条 CDN 优选分组包含非法字符"
				}
			}
		}
		enabled := entries[i].Enabled
		if enabled == nil {
			enabled = cdnBoolPtr(true) // 缺省启用（兼容旧请求/旧前端）
		}
		// primary 仅为旧 JSON 兼容：新请求不再写入主备语义，统一置 false。
		entries[i] = singboxCDNEntry{Name: name, Domain: domain, Primary: false, Enabled: enabled, PreferGroup: group}
	}
	return finalizeCDNEntries(entries), ""
}

// singboxNodeFromRender 把渲染节点转成链接节点，保留多 CDN 条目。
func singboxNodeFromRender(node singboxRenderNode) singboxNode {
	return singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain, CDNEntries: node.CDNEntries}
}

// cdnLinkTarget 是一条客户端链接的实际连接地址。
type cdnLinkTarget struct {
	Host      string // 已含 :443
	Direct    bool   // true=CDN 默认域名直连；false=优选地址
	Preferred bool
	Remark    string
}

// cdnLinkTargets 计算某 CDN 条目应生成的连接地址：
//   - 旧单域名（legacy）保持原模板 cdn_prefer 行为（含 mode/limit/random）；
//   - 新条目 Cloudflare 未显式 prefer_group 时自动使用 cloudflare 优选组；
//   - 有优选地址时同时保留默认域名直连（节点-CDN名称）并展开优选（节点-CDN名称-1…）；
//   - 为空 → 直接用 domain:443，绝不套用其它分组（避免 CF 条目使用 ESA 优选 IP）。
func cdnLinkTargets(tpl *singboxTemplate, entry singboxCDNEntry, legacy bool) []cdnLinkTarget {
	direct := func(domain string) []cdnLinkTarget {
		return []cdnLinkTarget{{Host: net.JoinHostPort(domain, "443"), Direct: true}}
	}
	toTargets := func(list []cdnPrefer) []cdnLinkTarget {
		out := make([]cdnLinkTarget, 0, len(list))
		for _, p := range list {
			out = append(out, cdnLinkTarget{Host: net.JoinHostPort(p.Address, "443"), Preferred: true, Remark: p.Remark})
		}
		return out
	}

	if !legacy {
		group := strings.TrimSpace(entry.PreferGroup)
		// Cloudflare 条目默认使用 cloudflare 优选组；用户无需再单独填写 prefer_group。
		// 其它 CDN 名称暂不自动套用，避免 ESA/EO 误用 Cloudflare 优选地址。
		if group == "" && (strings.EqualFold(strings.TrimSpace(entry.Name), "Cloudflare") ||
			strings.EqualFold(strings.TrimSpace(entry.Name), "CF")) {
			group = cdnPreferredDefaultGroup
		}
		if group == "" {
			return direct(entry.Domain)
		}
		list := cdnPreferredForGroup(group)
		if len(list) == 0 {
			return direct(entry.Domain)
		}
		// 默认节点 + 优选版本：先保留 CDN 域名直连，再追加 -1…-N。
		out := direct(entry.Domain)
		out = append(out, toTargets(list)...)
		return out
	}

	// legacy：完全复刻旧单 CDN 行为（模板 cdn_prefer 配置 + 全局开关）。
	vlwsOpt := optMap(tpl.protoOptions(), "vlws")
	prefer := optMap(vlwsOpt, "cdn_prefer")
	preferOn := cdnPreferEnabled() || jsonInt(prefer["enabled"]) != 0
	if !preferOn {
		return direct(entry.Domain)
	}
	list := cdnPreferredForGroup(jsonStr(prefer["group"]))
	if jsonStr(prefer["mode"]) == "random" {
		rand.Shuffle(len(list), func(i, j int) { list[i], list[j] = list[j], list[i] })
		n := int(jsonInt(prefer["limit"]))
		if n <= 0 {
			n = 1
		}
		if n > len(list) {
			n = len(list)
		}
		list = list[:n]
	} else if limit := int(jsonInt(prefer["limit"])); limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	if len(list) == 0 {
		return direct(entry.Domain)
	}
	return toTargets(list)
}

// singboxCDNLinkTag 生成 CDN 链接 tag：新多 CDN 条目统一为「节点-CDN名称-目标序号」，
// 其中同名多条目先按 occurrence 区分（第二条第 1 个目标为 -Cloudflare-2-1），再追加
// 该条目的优选地址序号（单目标也会追加 -1）。旧单域名保持 -cdn / -cdn-1 兼容。
func singboxCDNLinkTag(baseTag string, entry singboxCDNEntry, occurrence, targetIdx, targetCount int, direct, legacy bool) string {
	var tag string
	if legacy {
		tag = baseTag + "-cdn"
		if targetCount > 1 {
			tag += "-" + strconv.Itoa(targetIdx+1)
		}
		return tag
	}
	tag = baseTag + "-" + cdnEntryLabel(entry)
	if occurrence > 0 {
		tag += "-" + strconv.Itoa(occurrence+1)
	}
	// 默认节点名：节点-CDN名称；优选版本：节点-CDN名称-1、-2……
	if direct {
		return tag
	}
	tag += "-" + strconv.Itoa(targetIdx+1)
	return tag
}

// singboxClientLinks 生成客户端订阅链接（WARP 已从模板移除，这里不生成 warp 变体）。
func singboxClientLinks(tpl *singboxTemplate, node singboxNode, users []sbUser) []map[string]any {
	ports := singboxPorts(tpl)
	domain := strings.TrimSpace(node.Domain)
	cdnDomain := strings.TrimSpace(node.CDNDomain)
	allEntries := effectiveCDNEntries(node)
	// 停用的 CDN 不生成任何订阅链接；全部停用时 entries 为空（无 CDN 链接）。
	entries := enabledCDNEntries(allEntries)
	// 旧单域名（含迁移回填后 name="" 的单条目）沿用原模板 cdn_prefer 展开逻辑。
	// 新条目 name 必填（normalizeCDNInput 已校验），因此“单条无名称”只可能来自旧数据。
	legacyCDN := false
	if len(allEntries) == 1 && strings.TrimSpace(allEntries[0].Name) == "" && strings.TrimSpace(allEntries[0].PreferGroup) == "" {
		legacyCDN = true
	}
	if len(node.CDNEntries) == 0 && cdnDomain != "" {
		legacyCDN = true
	}
	if primary := cdnPrimaryDomain(allEntries); primary != "" {
		cdnDomain = primary
	}
	enc := url.QueryEscape
	out := []map[string]any{}
	multi := len(users) > 1
	cdnTagSeen := map[string]int{}
	entryOccurrence := make([]int, len(entries))
	{
		labelCount := map[string]int{}
		for i, e := range entries {
			label := cdnEntryLabel(e)
			entryOccurrence[i] = labelCount[label]
			labelCount[label]++
		}
	}

	engineSb := tpl.engineOn("singbox")
	opts := tpl.protoOptions()
	realityPub := tpl.realityPublicKey()
	wsPath := firstNonEmpty(strings.TrimSpace(jsonStr(optMap(opts, "vlws")["path"])), "/vlws")
	realitySni := firstNonEmpty(strings.TrimSpace(jsonStr(optMap(opts, "reality")["server_name"])), strings.TrimSpace(tpl.RealitySNI), "www.microsoft.com")
	wsSni := firstNonEmpty(strings.TrimSpace(tpl.WSSNI), "www.bing.com")

	add := func(protocol string, port int, user, link, note string) {
		out = append(out, map[string]any{
			"id": len(out) + 1, "protocol": protocol, "port": port, "user": user, "link": link, "note": note,
		})
	}

	for _, u := range users {
		suffix := ""
		if multi {
			suffix = "-" + u.Name
		}
		tag := func(proto string) string { return node.Name + suffix + "-" + proto }

		if engineSb && tpl.EnableSS != 0 && u.SSPassword != "" {
			auth := base64.RawURLEncoding.EncodeToString([]byte(
				firstNonEmpty(strings.TrimSpace(tpl.SSMethod), singboxDefaultSSMethod) + ":" +
					strings.TrimSpace(tpl.SSServerKey) + ":" + u.SSPassword))
			add("shadowsocks", ports["ss"], u.Name,
				"ss://"+auth+"@"+domain+":"+strconv.Itoa(ports["ss"])+"#"+enc(tag("ss")),
				"SS2022 多用户：密码 = 服务器密钥:用户密钥")
		}
		if engineSb && tpl.EnableAnyTLS != 0 {
			link := "anytls://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["anytls"]) +
				"/?security=tls&sni=" + enc(wsSni) + "&insecure=1#" + enc(tag("anytls"))
			add("anytls", ports["anytls"], u.Name, link, "自签证书：客户端需允许不安全")
		}
		if engineSb && tpl.EnableHY2 != 0 {
			link := "hysteria2://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["hy2"]) +
				"/?sni=" + enc(wsSni) + "&insecure=1&alpn=h3#" + enc(tag("hy2"))
			add("hysteria2", ports["hy2"], u.Name, link, "UDP 端口：注意放行与 NAT 映射")
		}
		if engineSb && tpl.EnableReality != 0 && realityPub != "" {
			link := "vless://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["reality"]) +
				"/?security=reality&encryption=none&pbk=" + enc(realityPub) +
				"&fp=chrome&sni=" + enc(realitySni) + "&sid=" + enc(tpl.RealityShortID) +
				"&spx=%2F&flow=xtls-rprx-vision&type=tcp#" + enc(tag("reality"))
			add("vless-reality", ports["reality"], u.Name, link, "pbk / sid 必须与服务端一致")
		}
		if engineSb && tpl.EnableVLWS != 0 && domain != "" {
			link := "vless://" + u.UUID + "@" + domain + ":" + strconv.Itoa(ports["vlws"]) +
				"/?security=tls&encryption=none&sni=" + enc(domain) + "&fp=chrome&type=ws&path=" +
				enc(wsPath) + "&host=" + enc(domain) + "#" + enc(tag("vlws"))
			add("vless-ws", ports["vlws"], u.Name, link, "直连源站 "+strconv.Itoa(ports["vlws"])+"；依赖 80/443 签证书")

			// 多 CDN 条目：每条目一条（或按优选分组多条）链接，按条目原顺序生成。
			for i, entry := range entries {
				targets := cdnLinkTargets(tpl, entry, legacyCDN)
				cdnBaseTag := node.Name + suffix
				if legacyCDN {
					cdnBaseTag = tag("vlws")
				}
				prefIdx := 0
				for ti, tgt := range targets {
					idx := ti
					if !legacyCDN && !tgt.Direct {
						idx = prefIdx
						prefIdx++
					} else if !legacyCDN {
						idx = 0
					}
					tagText := singboxCDNLinkTag(cdnBaseTag, entry, entryOccurrence[i], idx, len(targets), tgt.Direct, legacyCDN)
					// 兜底去重（例如同名同域名但实际不同条目已被域名唯一校验拦住；
					// 这里主要防止优选地址重复产生相同 tag）。
					if n := cdnTagSeen[tagText]; n > 0 {
						tagText += "-" + strconv.Itoa(n+1)
					}
					cdnTagSeen[tagText]++
					note := "CDN 优选："
					if !tgt.Preferred {
						note = "CDN 直连："
					}
					note += tgt.Host + "（SNI/Host 保持 " + entry.Domain
					if entry.Name != "" {
						note += "，条目 " + entry.Name
					}
					note += "）"
					item := map[string]any{
						"id": 0, "protocol": "vless-ws", "port": 443, "user": u.Name,
						"link": "vless://" + u.UUID + "@" + tgt.Host + "/?security=tls&encryption=none&sni=" +
							enc(entry.Domain) + "&fp=chrome&type=ws&path=" + enc(wsPath) + "&host=" + enc(entry.Domain) +
							"#" + enc(tagText),
						"cdn": true, "cdn_name": entry.Name, "note": note,
					}
					if entry.PreferGroup != "" {
						item["prefer_group"] = entry.PreferGroup
					}
					if tgt.Preferred {
						item["cdn_preferred"] = true
						if tgt.Remark != "" {
							item["remark"] = tgt.Remark
						}
					}
					item["id"] = len(out) + 1
					out = append(out, item)
				}
			}
		}
		if engineSb && tpl.EnableVMess != 0 && domain != "" {
			link := "tuic://" + enc(u.UUID) + ":" + enc(u.UUID) + "@" + domain + ":" + strconv.Itoa(ports["vmess"]) +
				"/?congestion_control=bbr&alpn=h3&sni=" + enc(domain) + "&allow_insecure=1#" + enc(tag("tuic"))
			add("tuic", ports["vmess"], u.Name, link, "TUIC v5（QUIC，UDP 13704）")
		}
	}
	if len(entries) > 0 && len(out) > singboxMaxCDNLinks {
		out = out[:singboxMaxCDNLinks]
	}
	return out
}
