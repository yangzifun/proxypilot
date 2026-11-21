package api

import (
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	if entries[0].Domain != "cdn.example.com" || entries[0].Name != "" || !cdnEntryEnabled(entries[0]) {
		t.Fatalf("旧单域名归一化错误：%+v", entries[0])
	}
	if got := mapStr(out, "cdn_domain", ""); got != "cdn.example.com" {
		t.Fatalf("cdn_domain 应为主域名，实际 %q", got)
	}
	if got := strings.TrimSpace(mapStr(out, "cdn_entries", "")); got != `[{"name":"","domain":"cdn.example.com","primary":false,"enabled":true,"prefer_group":""}]` {
		t.Fatalf("cdn_entries 序列化错误：%s", got)
	}
}

func TestNormalizeCDNInputLegacyDomains(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_domains": []any{"A.example.com", "b.example.com"}})
	entries := entriesOfInput(t, out)
	if len(entries) != 2 {
		t.Fatalf("cdn_domains 应转 2 条条目，实际 %d", len(entries))
	}
	if entries[0].Domain != "a.example.com" || entries[1].Domain != "b.example.com" ||
		!cdnEntryEnabled(entries[0]) || !cdnEntryEnabled(entries[1]) {
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
	// primary 已忽略：保持原顺序，不因 primary=true 把 ESA 提前。
	if entries[0].Domain != "cf.example.com" || entries[1].Domain != "esa.example.com" || entries[2].Domain != "eo.example.com" {
		t.Fatalf("条目顺序应保持原始顺序：%v", entries)
	}
	if entries[0].PreferGroup != "cloudflare" || entries[2].PreferGroup != "" {
		t.Fatalf("prefer_group 归一化错误：%v", entries)
	}
	if got := mapStr(out, "cdn_domain", ""); got != "cf.example.com" {
		t.Fatalf("cdn_domain 应取第一个启用条目，实际 %q", got)
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
	if len(entries) != 2 || entries[0].Domain != "cf.example.com" || entries[1].Domain != "esa.example.com" {
		t.Fatalf("响应 cdn_entries 应保持原顺序：%#v", out["cdn_entries"])
	}
	domains, _ := out["cdn_domains"].([]string)
	if len(domains) != 2 || domains[0] != "cf.example.com" || domains[1] != "esa.example.com" {
		t.Fatalf("响应 cdn_domains 错误：%#v", out["cdn_domains"])
	}
	if got, _ := out["cdn_domain"].(string); got != "cf.example.com" {
		t.Fatalf("响应 cdn_domain 应取第一个启用条目，实际 %q", got)
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
	if len(cdnLinks) != 6 {
		t.Fatalf("期望 6 条 CDN 链接（CF 默认+2优选、ESA 默认+1优选、EO 默认），实际 %d：%#v", len(cdnLinks), cdnLinks)
	}
	// Cloudflare：默认节点维持 domain:443，并自动展开 cloudflare 优选的 -1/-2。
	cfLink := mapStr(cdnLinks[0], "link", "")
	if !strings.Contains(cfLink, "@cf.example.com:443") || !strings.Contains(cfLink, "sni=cf.example.com") || !strings.Contains(cfLink, "host=cf.example.com") {
		t.Fatalf("主 CDN CF 链接错误：%s", cfLink)
	}
	if !strings.Contains(cfLink, "Cloudflare") {
		t.Fatalf("CF 链接 tag 应归一化为 Cloudflare：%s", cfLink)
	}
	cfPref1 := mapStr(cdnLinks[1], "link", "")
	cfPref2 := mapStr(cdnLinks[2], "link", "")
	if !strings.Contains(cfPref1, "162.159.198.1:443") || !strings.Contains(cfPref2, "162.159.198.2:443") {
		t.Fatalf("CF 优选地址未正确展开：%s / %s", cfPref1, cfPref2)
	}
	// ESA 只展开自己分组，不能用 cloudflare 的优选地址；默认域名也会保留。
	esaLink := mapStr(cdnLinks[3], "link", "")
	if !strings.Contains(esaLink, "@esa.example.com:443") || !strings.Contains(esaLink, "sni=esa.example.com") {
		t.Fatalf("ESA 默认链接错误：%s", esaLink)
	}
	esaPref := mapStr(cdnLinks[4], "link", "")
	if !strings.Contains(esaPref, "@esa.edge.example.net:443") || !strings.Contains(esaPref, "sni=esa.example.com") {
		t.Fatalf("ESA 链接应展开 esa 分组并保持 SNI：%s", esaPref)
	}
	if strings.Contains(esaPref, "162.159.198.") || !strings.Contains(esaPref, "ESA") {
		t.Fatalf("ESA 链接串用了其它分组/缺少名称：%s", esaPref)
	}
	// EO 没有 prefer_group，直接 domain:443。
	eoLink := mapStr(cdnLinks[5], "link", "")
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
		if !strings.Contains(tag, "Cloudflare") {
			t.Fatalf("tag 应把旧名称 CF 归一化为 Cloudflare：%s", tag)
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
	if len(node.CDNEntries) != 2 || node.CDNEntries[0].Name != "Cloudflare" || node.CDNEntries[1].Name != "ESA" {
		t.Fatalf("inputToNode 未携带 cdn_entries：%#v", node.CDNEntries)
	}
	if node.CDNDomain != "cf.example.com" {
		t.Fatalf("inputToNode CDNDomain 应取第一个启用条目，实际 %q", node.CDNDomain)
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

// ---- enabled（启用/停用）----

func TestNormalizeCDNInputEnabled(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_entries": []any{
		map[string]any{"name": "CF", "domain": "cf.example.com", "primary": true, "enabled": false},
		map[string]any{"name": "ESA", "domain": "esa.example.com"},
		map[string]any{"name": "EO", "domain": "eo.example.com", "enabled": true},
	}})
	entries := entriesOfInput(t, out)
	if len(entries) != 3 {
		t.Fatalf("应 3 条条目，实际 %d", len(entries))
	}
	// primary 已忽略：条目保持原顺序（CF 仍在前，虽然被停用）；cdn_domain 取第一个启用条目。
	if entries[0].Domain != "cf.example.com" || entries[1].Domain != "esa.example.com" || entries[2].Domain != "eo.example.com" {
		t.Fatalf("条目顺序应保持原始顺序：%v", entries)
	}
	if cdnEntryEnabled(entries[0]) {
		t.Fatalf("CF 应停用：%+v", entries[0])
	}
	if !cdnEntryEnabled(entries[1]) || !cdnEntryEnabled(entries[2]) {
		t.Fatalf("ESA/EO 应启用：%v", entries)
	}
	if got := mapStr(out, "cdn_domain", ""); got != "esa.example.com" {
		t.Fatalf("cdn_domain 应取第一个启用条目，实际 %q", got)
	}
}

func TestNormalizeCDNInputEnabledDefaultAndInvalid(t *testing.T) {
	// 缺省 enabled = true（旧数据/旧请求）。
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_domains": []any{"a.example.com", "b.example.com"}})
	for _, e := range entriesOfInput(t, out) {
		if !cdnEntryEnabled(e) {
			t.Fatalf("旧输入缺省应启用：%+v", e)
		}
	}
	// 非法 enabled（字符串）应拒绝。
	body := map[string]any{
		"name": "n1", "server_id": float64(1),
		"cdn_entries": []any{map[string]any{"name": "CF", "domain": "cf.example.com", "enabled": "yes"}},
	}
	if _, msg := normalizeSingboxNodeInput(body); !strings.Contains(msg, "cdn_entries 格式无效") {
		t.Fatalf("非法 enabled 应拒绝，实际 %q", msg)
	}
}

func TestSingboxClientLinksDisabledEntries(t *testing.T) {
	tpl := &singboxTemplate{ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1}
	disabled := false
	enabled := true
	node := singboxNode{
		Name: "hk", Domain: "hk.example.com",
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "cf.example.com", Primary: true, Enabled: &enabled},
			{Name: "ESA", Domain: "esa.example.com", Enabled: &disabled},
			{Name: "EO", Domain: "eo.example.com", Enabled: &enabled},
		},
	}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 2 {
		t.Fatalf("停用 ESA 后应 2 条 CDN 链接，实际 %d：%#v", len(cdnLinks), cdnLinks)
	}
	joined := ""
	for _, l := range cdnLinks {
		joined += mapStr(l, "link", "") + "\n"
	}
	if strings.Contains(joined, "esa.example.com") || !strings.Contains(joined, "cf.example.com") || !strings.Contains(joined, "eo.example.com") {
		t.Fatalf("停用条目不应生成链接：%s", joined)
	}
	// 全部停用：不生成任何 CDN 链接，但仍保留直连 vlws。
	disabledNode := singboxNode{
		Name: "hk", Domain: "hk.example.com",
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "cf.example.com", Primary: true, Enabled: &disabled},
			{Name: "ESA", Domain: "esa.example.com", Enabled: &disabled},
		},
	}
	all := singboxClientLinks(tpl, disabledNode, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	if got := cdnLinksOf(all); len(got) != 0 {
		t.Fatalf("全部停用不应生成 CDN 链接：%#v", got)
	}
	if len(all) != 1 || mapStr(all[0], "protocol", "") != "vless-ws" {
		t.Fatalf("直连 vlws 应保留：%#v", all)
	}
}

func TestSingboxNodeToJSONEnabledFields(t *testing.T) {
	row := map[string]any{
		"id": float64(1), "name": "hk",
		"cdn_entries": `[{"name":"CF","domain":"cf.example.com","primary":true,"enabled":true,"prefer_group":""},{"name":"ESA","domain":"esa.example.com","primary":false,"enabled":false,"prefer_group":""}]`,
	}
	out := singboxNodeToJSON(row)
	entries, _ := out["cdn_entries"].([]singboxCDNEntry)
	if len(entries) != 2 {
		t.Fatalf("响应条目数错误：%#v", out["cdn_entries"])
	}
	foundDisabled := false
	for _, e := range entries {
		if e.Name == "ESA" && e.Enabled != nil && !*e.Enabled {
			foundDisabled = true
		}
	}
	if !foundDisabled {
		t.Fatalf("响应应保留 enabled=false：%#v", entries)
	}
	domains, _ := out["cdn_domains"].([]string)
	if len(domains) != 1 || domains[0] != "cf.example.com" {
		t.Fatalf("cdn_domains 应只含启用条目：%#v", out["cdn_domains"])
	}
	if got, _ := out["cdn_domain"].(string); got != "cf.example.com" {
		t.Fatalf("cdn_domain 应为主 CDN，实际 %q", got)
	}
}

// NodeBindings 编辑回填必须拿到 cdN_entries（名称/启用状态），不能只有旧 sb_cdn。
func TestNodeBindingsIncludesCDNEntries(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if !strings.Contains(q, "FROM servers") {
			return &fakeRows{}, nil
		}
		return fakeRowsSet(
			[]string{"server_id", "server_name", "sb_id", "sb_name", "sb_cdn", "sb_cdn_entries", "sb_uuid_list", "sb_last_apply"},
			[]driver.Value{
				int64(1), "srv-1", int64(7), "hk-node", "cf.example.com",
				`[{"name":"CF","domain":"cf.example.com","primary":true,"enabled":true,"prefer_group":"cloudflare"},{"name":"ESA","domain":"esa.example.com","primary":false,"enabled":false,"prefer_group":""}]`,
				`["11111111-1111-4111-8111-111111111111"]`, int64(123),
			},
		), nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.nodeBindingsList(rec, httptest.NewRequest(http.MethodGet, "/api/nodes/bindings", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("响应 JSON 解析失败：%v", err)
	}
	data, _ := body["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("data 长度 = %d", len(data))
	}
	row, _ := data[0].(map[string]any)
	entries, _ := row["sb_cdn_entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("sb_cdn_entries 应为 2 条可解析数组，实际 %#v", row["sb_cdn_entries"])
	}
	first, _ := entries[0].(map[string]any)
	if mapStr(first, "name", "") != "Cloudflare" || mapStr(first, "domain", "") != "cf.example.com" || !cdnEntryEnabledJSON(first) {
		t.Fatalf("第一条 CDN 名称/启用状态丢失：%#v", first)
	}
	second, _ := entries[1].(map[string]any)
	if mapStr(second, "name", "") != "ESA" || cdnEntryEnabledJSON(second) {
		t.Fatalf("第二条 CDN 应停用且保留名称：%#v", second)
	}
	if got := mapStr(row, "sb_cdn", ""); got != "cf.example.com" {
		t.Fatalf("sb_cdn 兼容字段应为主域名，实际 %q", got)
	}
	domains, _ := row["sb_cdn_domains"].([]any)
	if len(domains) != 1 || domains[0] != "cf.example.com" {
		t.Fatalf("sb_cdn_domains 应只含启用条目：%#v", row["sb_cdn_domains"])
	}
}

func cdnEntryEnabledJSON(v map[string]any) bool {
	if b, ok := v["enabled"].(bool); ok {
		return b
	}
	return true // 缺省启用
}

func TestSingboxClientLinksPrimaryIgnored(t *testing.T) {
	tpl := &singboxTemplate{ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1}
	disabled, enabled := false, true
	node := singboxNode{
		Name: "hk", Domain: "hk.example.com",
		// primary 仅为旧字段兼容，不再影响启用/顺序/fallback。
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "cf.example.com", Primary: true, Enabled: &disabled},
			{Name: "ESA", Domain: "esa.example.com", Primary: true, Enabled: &enabled},
			{Name: "EO", Domain: "eo.example.com", Enabled: &enabled},
		},
	}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 2 {
		t.Fatalf("primary 不应参与 fallback，仅按 enabled 过滤（应 2 条），实际 %d", len(cdnLinks))
	}
	if !strings.Contains(mapStr(cdnLinks[0], "link", ""), "@esa.example.com:443") ||
		!strings.Contains(mapStr(cdnLinks[1], "link", ""), "@eo.example.com:443") {
		t.Fatalf("启用条目应按原顺序生成链接：%#v", cdnLinks)
	}
	for _, l := range cdnLinks {
		if strings.Contains(mapStr(l, "link", ""), "cf.example.com") {
			t.Fatalf("停用的 CF 不应生成链接：%s", mapStr(l, "link", ""))
		}
	}
}

// task-48：不再区分主/备，只按 enabled 过滤；多个 primary 也接受但被忽略。
func TestNormalizeCDNInputMultiplePrimaryAccepted(t *testing.T) {
	out := mustNormalizeNodeInput(t, map[string]any{"cdn_entries": []any{
		map[string]any{"name": "CF", "domain": "cf.example.com", "primary": true},
		map[string]any{"name": "ESA", "domain": "esa.example.com", "primary": true},
		map[string]any{"name": "EO", "domain": "eo.example.com", "primary": true},
	}})
	entries := entriesOfInput(t, out)
	if len(entries) != 3 {
		t.Fatalf("多个 primary 不应被拒绝，实际 %d 条：%v", len(entries), entries)
	}
	// 顺序保持原顺序；primary 原样保留但不影响 cdn_domain（取第一个启用条目）。
	if entries[0].Domain != "cf.example.com" || entries[1].Domain != "esa.example.com" || entries[2].Domain != "eo.example.com" {
		t.Fatalf("primary 不应改变条目顺序：%v", entries)
	}
	if got := mapStr(out, "cdn_domain", ""); got != "cf.example.com" {
		t.Fatalf("cdn_domain 应取第一个启用条目，实际 %q", got)
	}
}

func TestSingboxClientLinksOnlyEnabledInOrder(t *testing.T) {
	tpl := &singboxTemplate{ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1}
	disabled, enabled := false, true
	// 两条启用（无 primary 字段）+ 一条停用，顺序固定。
	node := singboxNode{
		Name: "hk", Domain: "hk.example.com",
		CDNEntries: []singboxCDNEntry{
			{Name: "CF", Domain: "cf.example.com", Enabled: &enabled},
			{Name: "ESA", Domain: "esa.example.com", Enabled: &disabled},
			{Name: "EO", Domain: "eo.example.com", Enabled: &enabled},
		},
	}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	cdnLinks := cdnLinksOf(links)
	if len(cdnLinks) != 2 {
		t.Fatalf("2 启用 + 1 停用应生成 2 条链接，实际 %d", len(cdnLinks))
	}
	if !strings.Contains(mapStr(cdnLinks[0], "link", ""), "@cf.example.com:443") ||
		!strings.Contains(mapStr(cdnLinks[1], "link", ""), "@eo.example.com:443") {
		t.Fatalf("应只按启用条目的原顺序生成：%#v", cdnLinks)
	}
	for _, l := range cdnLinks {
		if strings.Contains(mapStr(l, "link", ""), "esa.example.com") {
			t.Fatalf("停用条目不应生成链接：%s", mapStr(l, "link", ""))
		}
	}
}

// 新多 CDN tag：默认节点 节点-CDN名称，优选版本 节点-CDN名称-1…；legacy 保持旧格式。
func TestSingboxCDNLinkTagDefaultAndPreferred(t *testing.T) {
	entry := singboxCDNEntry{Name: "Cloudflare"}
	cases := []struct {
		name                               string
		base                               string
		occurrence, targetIdx, targetCount int
		direct, legacy                     bool
		want                               string
	}{
		{"默认节点", "彰化", 0, 0, 1, true, false, "彰化-Cloudflare"},
		{"优选第1个", "彰化", 0, 0, 2, false, false, "彰化-Cloudflare-1"},
		{"优选第2个", "彰化", 0, 1, 2, false, false, "彰化-Cloudflare-2"},
		{"同名第二条默认节点", "彰化", 1, 0, 1, true, false, "彰化-Cloudflare-2"},
		{"同名第二条优选第1个", "彰化", 1, 0, 1, false, false, "彰化-Cloudflare-2-1"},
		{"legacy单目标", "彰化-vlws", 0, 0, 1, false, true, "彰化-vlws-cdn"},
		{"legacy多目标第2个", "彰化-vlws", 0, 1, 2, false, true, "彰化-vlws-cdn-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := singboxCDNLinkTag(tc.base, entry, tc.occurrence, tc.targetIdx, tc.targetCount, tc.direct, tc.legacy)
			if got != tc.want {
				t.Fatalf("tag = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func cdnLinkTags(links []map[string]any) []string {
	out := []string{}
	for _, l := range links {
		link := mapStr(l, "link", "")
		idx := strings.LastIndex(link, "#")
		if idx < 0 {
			continue
		}
		tag, _ := url.QueryUnescape(link[idx+1:])
		out = append(out, tag)
	}
	return out
}

// 订阅链接输出：单 CDN、多 CDN、同名多条目、多优选地址的 tag 均须符合命名规则。
func TestSingboxClientLinksCDNTagNaming(t *testing.T) {
	setCDNTestCache(t, nil, false)
	tpl := &singboxTemplate{ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1}
	user := []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}}

	t.Run("单CDN单目标", func(t *testing.T) {
		node := singboxNode{Name: "彰化", Domain: "tw.example.com", CDNEntries: []singboxCDNEntry{
			{Name: "Cloudflare", Domain: "cf.example.com"},
		}}
		got := cdnLinkTags(cdnLinksOf(singboxClientLinks(tpl, node, user)))
		want := []string{"彰化-Cloudflare"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("tags = %v，期望 %v", got, want)
		}
	})

	t.Run("多CDN各自默认节点", func(t *testing.T) {
		node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNEntries: []singboxCDNEntry{
			{Name: "Cloudflare", Domain: "cf.example.com"},
			{Name: "ESA", Domain: "esa.example.com"},
			{Name: "EO", Domain: "eo.example.com"},
		}}
		got := cdnLinkTags(cdnLinksOf(singboxClientLinks(tpl, node, user)))
		want := []string{"hk-Cloudflare", "hk-ESA", "hk-EO"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("tags = %v，期望 %v", got, want)
		}
	})

	t.Run("同名多条目唯一", func(t *testing.T) {
		node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNEntries: []singboxCDNEntry{
			{Name: "Cloudflare", Domain: "a.example.com"},
			{Name: "Cloudflare", Domain: "b.example.com"},
		}}
		got := cdnLinkTags(cdnLinksOf(singboxClientLinks(tpl, node, user)))
		want := []string{"hk-Cloudflare", "hk-Cloudflare-2"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("tags = %v，期望 %v", got, want)
		}
	})

	t.Run("默认节点加多优选1和2", func(t *testing.T) {
		setCDNTestCache(t, []cdnPrefer{
			{Address: "162.159.198.1", Port: 443, Group: "cloudflare", Remark: "cf1"},
			{Address: "162.159.198.2", Port: 443, Group: "cloudflare", Remark: "cf2"},
		}, false)
		node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNEntries: []singboxCDNEntry{
			{Name: "Cloudflare", Domain: "cf.example.com"},
		}}
		got := cdnLinkTags(cdnLinksOf(singboxClientLinks(tpl, node, user)))
		want := []string{"hk-Cloudflare", "hk-Cloudflare-1", "hk-Cloudflare-2"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("tags = %v，期望 %v", got, want)
		}
	})
}
