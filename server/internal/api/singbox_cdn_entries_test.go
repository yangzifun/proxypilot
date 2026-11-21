package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// ---- 输入兼容：cdn_entries / cdn_domains / cdn_domain ----

func mustNormalizeNodeInput(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	body["name"] = "n1"
	body["server_id"] = float64(1)
	out, msg := normalizeSingboxNodeInput(body)
	if msg != "" {
		t.Fatalf("normalize 失败：%s（body=%v）", msg, body)
	}
	return out
}

func entriesOfInput(t *testing.T, out map[string]any) []singboxCDNEntry {
	t.Helper()
	return parseStoredCDNEntries(mapStr(out, "cdn_entries", ""), mapStr(out, "cdn_domain", ""))
}

func TestNormalizeCDNInputLegacyDomain(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_domain": " HTTPS://CDN.Example.com/x "})
	entries := entriesOfInput(t, out)
	if len(entries) != 1 {
		t.Fatalf("旧单域名应转 1 条条目，实际 %d：%v", len(entries), entries)
	}
	if entries[0].Domain != "cdn.example.com" || !entries[0].Primary || entries[0].Name != "" {
		t.Fatalf("旧单域名归一化错误：%+v", entries[0])
	}
	if got := mapStr(out, "cdn_domain", ""); got != "cdn.example.com" {
		t.Fatalf("cdn_domain 应为主域名，实际 %q", got)
	}
	if got := strings.TrimSpace(mapStr(out, "cdn_entries", "")); got != `[{"name":"","domain":"cdn.example.com","primary":true,"prefer_group":""}]` {
		t.Fatalf("cdn_entries 序列化错误：%s", got)
	}
}

func TestNormalizeCDNInputLegacyDomains(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_domains": []any{"A.example.com", "b.example.com"}})
	entries := entriesOfInput(t, out)
	if len(entries) != 2 {
		t.Fatalf("cdn_domains 应转 2 条条目，实际 %d", len(entries))
	}
	if entries[0].Domain != "a.example.com" || !entries[0].Primary || entries[1].Domain != "b.example.com" || entries[1].Primary {
		t.Fatalf("cdn_domains 归一化错误：%v", entries)
	}
	if !strings.Contains(mapStr(out, "cdn_entries", ""), "a.example.com") {
		t.Fatalf("cdn_entries 未包含旧域名：%s", mapStr(out, "cdn_entries", ""))
	}
}

func TestNormalizeCDNInputEntries(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_entries": []any{
		map[string]any{"name": "CF", "domain": "cf.example.com", "primary": false, "prefer_group": "cloudflare"},
		map[string]any{"name": "ESA", "domain": "esa.example.com", "primary": true, "prefer_group": ""},
		map[string]any{"name": "EO", "domain": "eo.example.com", "primary": false, "prefer_group": "  "},
	}})
	entries := entriesOfInput(t, out)
	if len(entries) != 3 {
		t.Fatalf("应 3 条条目，实际 %d", len(entries))
	}
	if !entries[0].Primary || entries[0].Domain != "esa.example.com" || entries[0].Name != "ESA" {
		t.Fatalf("主 CDN 应排最前：%v", entries)
	}
	if entries[1].Domain != "cf.example.com" || entries[1].PreferGroup != "cloudflare" {
		t.Fatalf("CF 条目丢失 prefer_group：%+v", entries[1])
	}
	if entries[2].Domain != "eo.example.com" || entries[2].PreferGroup != "" {
		t.Fatalf("EO prefer_group 空值应保留为空：%+v", entries[2])
	}
	if got := mapStr(out, "cdn_domain", ""); got != "esa.example.com" {
		t.Fatalf("cdn_domain = %q，期望主域名", got)
	}
}

func TestNormalizeCDNInputValidation(t *testing.T) {
	longName := strings.Repeat("A", singboxMaxCDNNameLen+1)
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"名称缺失", map[string]any{"cdn_entries": []any{map[string]any{"domain": "a.example.com"}}}, "第 1 条 CDN 名称不能为空"},
		{"名称过长", map[string]any{"cdn_entries": []any{map[string]any{"name": longName, "domain": "a.example.com"}}}, "第 1 条 CDN 名称不能超过"},
		{"名称换行", map[string]any{"cdn_entries": []any{map[string]any{"name": "CF\nX", "domain": "a.example.com"}}}, "第 1 条 CDN 名称包含非法字符"},
		{"域名无效", map[string]any{"cdn_entries": []any{map[string]any{"name": "CF", "domain": "not a domain"}}}, "第 1 条 CDN 域名无效"},
		{"域名是 IP", map[string]any{"cdn_entries": []any{map[string]any{"name": "CF", "domain": "1.2.3.4"}}}, "第 1 条 CDN 域名无效"},
		{"新条目域名带协议", map[string]any{"cdn_entries": []any{map[string]any{"name": "CF", "domain": "https://cf.example.com/x"}}}, "第 1 条 CDN 域名无效"},
		{"域名重复大小写", map[string]any{"cdn_entries": []any{
			map[string]any{"name": "CF", "domain": "a.example.com"},
			map[string]any{"name": "ESA", "domain": "A.EXAMPLE.COM"},
		}}, "第 2 条 CDN 域名重复"},
		{"多个主 CDN", map[string]any{"cdn_entries": []any{
			map[string]any{"name": "CF", "domain": "a.example.com", "primary": true},
			map[string]any{"name": "ESA", "domain": "b.example.com", "primary": true},
		}}, "只能设置一个主 CDN"},
		{"超过 8 条", map[string]any{"cdn_entries": func() []any {
			out := make([]any, 0, 9)
			for i := 0; i < 9; i++ {
				out = append(out, map[string]any{"name": "C" + strings.Repeat("F", i), "domain": "c" + string(rune('a'+i)) + ".example.com"})
			}
			return out
		}()}, "最多 8 个"},
		{"优选分组非法字符", map[string]any{"cdn_entries": []any{
			map[string]any{"name": "CF", "domain": "a.example.com", "prefer_group": "bad group"},
		}}, "优选分组包含非法字符"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			body["name"] = "n1"
			body["server_id"] = float64(1)
			_, msg := normalizeSingboxNodeInput(body)
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("错误文案 = %q，应包含 %q", msg, tc.want)
			}
		})
	}
}

// ---- 列表/详情响应字段 ----

func TestSingboxNodeToJSONMultiCDN(t *testing.T) {
	row := map[string]any{
		"id":          float64(1),
		"name":        "hk",
		"cdn_domain":  "cf.example.com",
		"cdn_entries": `[{"name":"CF","domain":"cf.example.com","primary":false,"prefer_group":"cloudflare"},{"name":"ESA","domain":"esa.example.com","primary":true,"prefer_group":""}]`,
	}
	out := singboxNodeToJSON(row)
	entries, _ := out["cdn_entries"].([]singboxCDNEntry)
	if len(entries) != 2 || !entries[0].Primary || entries[0].Domain != "esa.example.com" {
		t.Fatalf("响应 cdn_entries 主 CDN 应排最前：%#v", out["cdn_entries"])
	}
	domains, _ := out["cdn_domains"].([]string)
	if len(domains) != 2 || domains[0] != "esa.example.com" || domains[1] != "cf.example.com" {
		t.Fatalf("响应 cdn_domains 错误：%#v", out["cdn_domains"])
	}
	if got, _ := out["cdn_domain"].(string); got != "esa.example.com" {
		t.Fatalf("响应 cdn_domain 应为主域名，实际 %q", got)
	}
	// JSON 可正常序列化（前端拿到数组，而不是字符串）。
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("响应 JSON 序列化失败：%v", err)
	}
	if !strings.Contains(string(raw), `"cdn_entries":[]`) && len(entries) == 0 {
		t.Fatalf("空 CDN 时 cdn_entries 应为 []：%s", raw)
	}
}

func TestSingboxNodeToJSONEmptyCDNArrays(t *testing.T) {
	out := singboxNodeToJSON(map[string]any{"id": float64(1), "name": "n1"})
	raw, _ := json.Marshal(out)
	if !strings.Contains(string(raw), `"cdn_entries":[]`) || !strings.Contains(string(raw), `"cdn_domains":[]`) {
		t.Fatalf("空 CDN 时数组字段不能为 null：%s", raw)
	}
}

// ---- 链接生成 ----

func setCDNTestCache(t *testing.T, list []cdnPrefer, globalOn bool) {
	t.Helper()
	cdnPreferMu.Lock()
	oldCache, oldOn := cdnPreferCache, cdnPreferGlobalOn
	cdnPreferCache, cdnPreferGlobalOn = list, globalOn
	cdnPreferMu.Unlock()
	t.Cleanup(func() {
		cdnPreferMu.Lock()
		cdnPreferCache, cdnPreferGlobalOn = oldCache, oldOn
		cdnPreferMu.Unlock()
	})
}

func cdnLinksOf(links []map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, l := range links {
		if b, _ := l["cdn"].(bool); b {
			out = append(out, l)
		}
	}
	return out
}

func TestSingboxClientLinksMultiCDN(t *testing.T) {
	setCDNTestCache(t, []cdnPrefer{
		{Address: "162.159.198.1", Port: 443, Group: "cloudflare", Remark: "cf"},
		{Address: "162.159.198.2", Port: 443, Group: "cloudflare"},
		{Address: "esa.edge.example.net", Port: 443, Group: "esa", Remark: "esa"},
		{Address: "tencent.edge.example.net", Port: 443, Group: "tencent"},
	}, false)

	tpl := &singboxTemplate{
		ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1,
		ProtocolOptions: `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":1,"group":"cloudflare"}}}`,
	}
	node := singboxNode{
		Name: "hk", Domain: "hk.example.com", CDNDomain: "cf.example.com",
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "cf.example.com", Primary: true, PreferGroup: ""},
			{Name: "ESA", Domain: "esa.example.com", PreferGroup: "esa"},
			{Name: "EO", Domain: "eo.example.com"},
		},
	}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 3 {
		t.Fatalf("期望 3 条 CDN 链接（CF 直连、ESA 1 优选、EO 直连），实际 %d：%#v", len(cdnLinks), cdnLinks)
	}
	// 主 CDN 排最前，且未设置 prefer_group 时必须是 domain:443（不能套 cloudflare 优选）。
	cfLink := mapStr(cdnLinks[0], "link", "")
	if !strings.Contains(cfLink, "@cf.example.com:443") || !strings.Contains(cfLink, "sni=cf.example.com") || !strings.Contains(cfLink, "host=cf.example.com") {
		t.Fatalf("主 CDN CF 链接错误：%s", cfLink)
	}
	if !strings.Contains(cfLink, "CF") {
		t.Fatalf("CF 链接 tag 应包含名称：%s", cfLink)
	}
	// ESA 只展开自己分组，不能用 cloudflare 的优选地址。
	esaLink := mapStr(cdnLinks[1], "link", "")
	if !strings.Contains(esaLink, "@esa.edge.example.net:443") || !strings.Contains(esaLink, "sni=esa.example.com") {
		t.Fatalf("ESA 链接应展开 esa 分组并保持 SNI：%s", esaLink)
	}
	if strings.Contains(esaLink, "162.159.198.") || !strings.Contains(esaLink, "ESA") {
		t.Fatalf("ESA 链接串用了其它分组/缺少名称：%s", esaLink)
	}
	// EO 没有 prefer_group，直接 domain:443。
	eoLink := mapStr(cdnLinks[2], "link", "")
	if !strings.Contains(eoLink, "@eo.example.com:443") || strings.Contains(eoLink, "162.159.") {
		t.Fatalf("EO 未设置分组时应直连域名：%s", eoLink)
	}
	// tag 唯一性
	seen := map[string]bool{}
	for _, l := range links {
		link := mapStr(l, "link", "")
		idx := strings.LastIndex(link, "#")
		if idx < 0 {
			continue
		}
		tag := link[idx+1:]
		if seen[tag] {
			t.Fatalf("tag 重复：%s", tag)
		}
		seen[tag] = true
	}
}

func TestSingboxClientLinksSameNameUniqueTags(t *testing.T) {
	tpl := &singboxTemplate{ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1}
	node := singboxNode{
		Name: "hk", Domain: "hk.example.com",
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "a.example.com", Primary: true},
			{Name: "CF", Domain: "b.example.com"},
		},
	}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 2 {
		t.Fatalf("期望 2 条同名 CDN 链接，实际 %d", len(cdnLinks))
	}
	tags := map[string]bool{}
	for _, l := range cdnLinks {
		link := mapStr(l, "link", "")
		idx := strings.LastIndex(link, "#")
		if idx < 0 {
			t.Fatalf("链接缺少 tag：%s", link)
		}
		tag := link[idx+1:]
		if !strings.Contains(tag, "CF") {
			t.Fatalf("tag 应包含名称 CF：%s", tag)
		}
		if tags[tag] {
			t.Fatalf("同名多域名 tag 必须唯一：%s", tag)
		}
		tags[tag] = true
	}
}

func TestSingboxClientLinksLegacySingleCDNDomain(t *testing.T) {
	setCDNTestCache(t, []cdnPrefer{
		{Address: "162.159.198.1", Port: 443, Group: "cloudflare"},
	}, false)
	tpl := &singboxTemplate{
		ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1,
		ProtocolOptions: `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":1,"group":"cloudflare"}}}`,
	}
	node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNDomain: "cdn.example.com"}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 1 {
		t.Fatalf("旧单域名 + 全局优选开启应有 1 条优选链接，实际 %d", len(cdnLinks))
	}
	link := mapStr(cdnLinks[0], "link", "")
	if !strings.Contains(link, "162.159.198.1:443") || !strings.Contains(link, "sni=cdn.example.com") {
		t.Fatalf("旧单域名优先链接错误：%s", link)
	}
	if !strings.Contains(link, "hk-vlws-cdn") {
		t.Fatalf("旧单域名 tag 应保持兼容：%s", link)
	}
	// 迁移回填后的旧条目（单条 name=""）仍须沿用旧 cdn_prefer 展开，不能退化成直连。
	migrated := singboxNode{
		Name: "hk", Domain: "hk.example.com", CDNDomain: "cdn.example.com",
		CDNEntries: []singboxCDNEntry{{Domain: "cdn.example.com", Primary: true}},
	}
	tpl.ProtocolOptions = `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":1,"group":"cloudflare"}}}`
	links = singboxClientLinks(tpl, migrated, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks = cdnLinksOf(links)
	if len(cdnLinks) != 1 || !strings.Contains(mapStr(cdnLinks[0], "link", ""), "162.159.198.1:443") {
		t.Fatalf("迁移回填的旧条目应保持优选展开：%#v", cdnLinks)
	}
	// 未启用优选时回退 cdn.example.com:443（旧行为）。
	tpl.ProtocolOptions = `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":0}}}`
	links = singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks = cdnLinksOf(links)
	if len(cdnLinks) != 1 || !strings.Contains(mapStr(cdnLinks[0], "link", ""), "@cdn.example.com:443") {
		t.Fatalf("关闭优选后应回退 cdn.example.com:443：%#v", cdnLinks)
	}
}

func TestCDNEntriesRejectIP(t *testing.T) {
	body := map[string]any{
		"name": "n1", "server_id": float64(1),
		"cdn_entries": []any{map[string]any{"name": "CF", "domain": "203.0.113.1"}},
	}
	if _, msg := normalizeSingboxNodeInput(body); !strings.Contains(msg, "域名无效") {
		t.Fatalf("IP 应被拒绝，实际 %q", msg)
	}
}

func TestInputToNodeCarriesCDNEntries(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_entries": []any{
		map[string]any{"name": "CF", "domain": "cf.example.com", "primary": true},
		map[string]any{"name": "ESA", "domain": "esa.example.com"},
	}})
	node := inputToNode(out)
	if len(node.CDNEntries) != 2 || node.CDNEntries[0].Name != "CF" || !node.CDNEntries[0].Primary {
		t.Fatalf("inputToNode 未携带 cdn_entries：%#v", node.CDNEntries)
	}
	if node.CDNDomain != "cf.example.com" {
		t.Fatalf("inputToNode CDNDomain 应为主域名，实际 %q", node.CDNDomain)
	}
	linkNode := singboxNodeFromRender(node)
	if len(linkNode.CDNEntries) != 2 {
		t.Fatalf("render->link 节点丢失条目：%#v", linkNode)
	}
}

func TestInputToNodeLegacyFallback(t *testing.T) {
	node := inputToNode(map[string]any{"name": "n1", "domain": "hk.example.com", "cdn_domain": "cdn.example.com"})
	if node.CDNDomain != "cdn.example.com" || len(node.CDNEntries) != 1 || node.CDNEntries[0].Domain != "cdn.example.com" {
		t.Fatalf("旧 cdn_domain 未回退：%#v", node)
	}
}
