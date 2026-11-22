package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
//  模板编辑器单测：YAML 解析/区段 patch/校验/受管 R2/草稿与发布/预览/运行时
// =====================================================================

const templateEditorSample = `# ProxyPilot 模板样例
port: 7890
socks-port: 7891
allow-lan: false
mode: rule
log-level: info
external-controller: 127.0.0.1:9090
profile:
  store-selected: true

rule-anchor:
  ip: &ip {type: http, behavior: ipcidr, interval: 300}
  domain: &domain {type: http, behavior: domain, interval: 300}

dns:
  enable: true
  listen: 0.0.0.0:1053
  nameserver:
    - https://1.1.1.1/dns-query
  nameserver-policy:
    "geosite:cn": https://223.5.5.5/dns-query

tun:
  enable: true
  stack: mixed
  dns-hijack:
    - any:53

proxies:
  - {name: 直连, type: direct}

proxy-groups:
  - name: 🚀 节点选择
    type: select
    proxies: [自动选择, 直连]
  - name: 自动选择
    type: url-test
    url: http://www.gstatic.com/generate_204
    interval: 300
    tolerance: 50

rules:
  - RULE-SET,domain-list,🚀 节点选择
  - MATCH,🚀 节点选择

rule-providers:
  domain-list:
    <<: *domain
    url: https://example.com/domain.mrs
`

// ---------------- 解析 ----------------

func TestParseTemplateYAMLBasic(t *testing.T) {
	doc, stats, err := parseTemplateYAML(templateEditorSample)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if stats.Anchors < 2 || stats.Aliases < 1 || stats.Comments < 1 {
		t.Fatalf("锚点/别名/注释统计异常：%+v", stats)
	}
	sections, err := sectionsFromNode(doc.Content[0], stats)
	if err != nil {
		t.Fatalf("区段解析失败：%v", err)
	}
	if sections.Summary.TopKeys == 0 || sections.Summary.ProxyGroups != 2 || sections.Summary.Rules != 2 {
		t.Fatalf("摘要异常：%+v", sections.Summary)
	}
	if sections.Summary.RuleProviders != 1 || sections.Summary.Proxies != 1 {
		t.Fatalf("摘要异常：%+v", sections.Summary)
	}
	// base 不含保留区段；emoji 名称保持
	if _, exists := sections.Base["proxy-groups"]; exists {
		t.Fatal("base 不应包含 proxy-groups")
	}
	if sections.Base["log-level"] != "info" {
		t.Fatalf("base.log-level = %v", sections.Base["log-level"])
	}
	groups, _ := sections.ProxyGroups.([]any)
	if len(groups) != 2 {
		t.Fatalf("proxy_groups = %v", sections.ProxyGroups)
	}
	if name, _ := groups[0].(map[string]any)["name"].(string); name != "🚀 节点选择" {
		t.Fatalf("emoji 名称丢失：%q", name)
	}
	// rule-providers 的 << 合并应展开出 url
	providers, _ := sections.RuleProviders.(map[string]any)
	entry, _ := providers["domain-list"].(map[string]any)
	if entry["url"] != "https://example.com/domain.mrs" || entry["behavior"] != "domain" {
		t.Fatalf("merge key 未展开：%v", entry)
	}
}

func TestParseTemplateYAMLRejects(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"空内容", "  ", "内容为空"},
		{"非 UTF-8", string([]byte{0xff, 0xfe, 0x00}), "UTF-8"},
		{"顶层数组", "- a\n- b\n", "顶层必须是 YAML map"},
		{"语法错误", "a: [1, 2\n", "YAML 解析失败"},
		{"自定义 tag", "a: !custom foo\n", "禁止自定义 YAML tag"},
		{"重复顶层键", "a: 1\na: 2\n", "顶层键重复"},
		{"超大内容", strings.Repeat("a: 1\n", (templateEditorMaxBytes/5)+10), "内容过大"},
	}
	for _, tc := range cases {
		if _, _, err := parseTemplateYAML(tc.content); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：期望错误包含 %q，得到 %v", tc.name, tc.want, err)
		}
	}
}

func TestParseTemplateYAMLAnchorLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("x: &a {v: 1}\naliases:\n")
	for i := 0; i <= templateEditorMaxAliases; i++ {
		b.WriteString("  - *a\n")
	}
	_, _, err := parseTemplateYAML(b.String())
	if err == nil || !strings.Contains(err.Error(), "别名过多") {
		t.Fatalf("超过别名上限应报错，得到 %v", err)
	}
}

func TestEncodeTemplateYAMLPreservesAnchorsComments(t *testing.T) {
	doc, _, err := parseTemplateYAML(templateEditorSample)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	content, err := encodeTemplateYAML(doc)
	if err != nil {
		t.Fatalf("编码失败：%v", err)
	}
	for _, want := range []string{"# ProxyPilot 模板样例", "&ip", "&domain", "*domain", "<<:"} {
		if !strings.Contains(content, want) {
			t.Fatalf("编码后丢失 %q：\n%s", want, content)
		}
	}
	// 重新解析后语义仍在
	if _, stats, err := parseTemplateYAML(content); err != nil || stats.Aliases < 1 {
		t.Fatalf("编码后重新解析失败：%v stats=%+v", err, stats)
	}
}

// ---------------- 区段 patch ----------------

func TestPatchTemplateSectionPreservesOthers(t *testing.T) {
	newGroups := `[
	  {"name":"🚀 节点选择","type":"select","proxies":["自动选择","直连"]},
	  {"name":"自动选择","type":"url-test","url":"http://www.gstatic.com/generate_204","interval":600},
	  {"name":"流媒体","type":"select","proxies":["自动选择"]}
	]`
	content, sections, warnings, verrs, err := patchTemplateSection(templateEditorSample, "proxy_groups", json.RawMessage(newGroups))
	if err != nil {
		t.Fatalf("patch 失败：%v", err)
	}
	if len(verrs) != 0 {
		t.Fatalf("patch 后不应有校验错误：%v", verrs)
	}
	_ = warnings
	if sections.Summary.ProxyGroups != 3 {
		t.Fatalf("区段摘要未更新：%+v", sections.Summary)
	}
	if !strings.Contains(content, "流媒体") || !strings.Contains(content, "# ProxyPilot 模板样例") {
		t.Fatalf("patch 后内容异常：\n%s", content)
	}
	// 其它区段的锚点/别名/规则仍在
	for _, want := range []string{"&ip", "*domain", "RULE-SET,domain-list", "rule-anchor:"} {
		if !strings.Contains(content, want) {
			t.Fatalf("patch 破坏其它区段（缺 %q）：\n%s", want, content)
		}
	}
}

func TestPatchTemplateSectionBaseSetAddDelete(t *testing.T) {
	data := `{"log-level":"debug","ipv6":false,"allow-lan":null}`
	content, sections, _, verrs, err := patchTemplateSection(templateEditorSample, "base", json.RawMessage(data))
	if err != nil || len(verrs) != 0 {
		t.Fatalf("base patch 失败：%v %v", err, verrs)
	}
	if sections.Base["log-level"] != "debug" || sections.Base["ipv6"] != false {
		t.Fatalf("base 修改未生效：%v", sections.Base)
	}
	if _, exists := sections.Base["allow-lan"]; exists {
		t.Fatal("null 应删除该顶层键")
	}
	if !strings.Contains(content, "port: 7890") {
		t.Fatalf("未修改的 base 键丢失：\n%s", content)
	}
}

func TestPatchTemplateSectionRulesAndProviders(t *testing.T) {
	content, sections, _, verrs, err := patchTemplateSection(templateEditorSample, "rules",
		json.RawMessage(`["DOMAIN-SUFFIX,google.com,🚀 节点选择","MATCH,🚀 节点选择"]`))
	if err != nil || len(verrs) != 0 {
		t.Fatalf("rules patch 失败：%v %v", err, verrs)
	}
	if sections.Summary.Rules != 2 {
		t.Fatalf("rules 摘要 = %+v", sections.Summary)
	}
	content, sections, _, verrs, err = patchTemplateSection(content, "rule-providers", json.RawMessage(
		`{"google":{"type":"http","behavior":"domain","url":"https://example.com/g.yaml","interval":300}}`))
	if err != nil || len(verrs) != 0 {
		t.Fatalf("providers patch 失败：%v %v", err, verrs)
	}
	if sections.Summary.RuleProviders != 1 {
		t.Fatalf("providers 摘要 = %+v", sections.Summary)
	}
}

func TestPatchTemplateSectionRejectsInvalid(t *testing.T) {
	// proxies 为系统注入位
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "proxies", json.RawMessage(`[]`)); err == nil {
		t.Fatal("proxies 区段应被拒绝")
	}
	// base 不允许写保留键
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "base", json.RawMessage(`{"rules":[]}`)); err == nil {
		t.Fatal("base 写保留键应被拒绝")
	}
	// 未知区段
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "unknown", json.RawMessage(`{}`)); err == nil {
		t.Fatal("未知区段应被拒绝")
	}
	// 类型不符
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "rules", json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("rules 非数组应被拒绝")
	}
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "proxy_groups", json.RawMessage(`{"a":1}`)); err == nil {
		t.Fatal("proxy_groups 非数组应被拒绝")
	}
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "rule_providers", json.RawMessage(`[]`)); err == nil {
		t.Fatal("rule_providers 非对象应被拒绝")
	}
	// 非法 JSON
	if _, _, _, _, err := patchTemplateSection(templateEditorSample, "rules", json.RawMessage(`[`)); err == nil {
		t.Fatal("非法 JSON 应被拒绝")
	}
}

// ---------------- 校验 ----------------

func TestValidateTemplateContentOK(t *testing.T) {
	res := validateTemplateContent(templateEditorSample)
	if !res.OK || len(res.Errors) != 0 {
		t.Fatalf("样例模板应通过校验：%+v", res)
	}
	if res.Summary.ProxyGroups != 2 || res.Summary.Rules != 2 {
		t.Fatalf("摘要异常：%+v", res.Summary)
	}
}

func TestValidateTemplateContentErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"语法错误", "a: [1\n", "YAML 解析失败"},
		{"proxies 写入用户节点", strings.Replace(templateEditorSample,
			"- {name: 直连, type: direct}", "- {name: 我的节点, type: trojan}", 1),
			"proxies 是系统注入位"},
		{"策略组类型非法", strings.Replace(templateEditorSample,
			"type: select", "type: unknown-type", 1), "type=unknown-type 不支持"},
		{"策略组重名", strings.Replace(templateEditorSample,
			"name: 自动选择\n    type: url-test", "name: 🚀 节点选择\n    type: url-test", 1), "名称重复"},
		{"provider behavior 非法", strings.Replace(templateEditorSample,
			"behavior: domain, interval: 300}", "behavior: nope, interval: 300}", 1), "behavior=nope 不支持"},
		{"provider 缺 url", strings.Replace(templateEditorSample,
			"url: https://example.com/domain.mrs", "", 1), "type=http 必须提供 url"},
		{"dns.enable 非布尔", strings.Replace(templateEditorSample, "enable: true\n  listen:", "enable: \"yes\"\n  listen:", 1), "dns.enable 必须是布尔值"},
		{"tun.stack 非法", strings.Replace(templateEditorSample, "stack: mixed", "stack: nope", 1), "tun.stack 仅支持"},
		{"rules 非字符串", strings.Replace(templateEditorSample,
			"  - MATCH,🚀 节点选择", "  - {a: 1}", 1), "rules[1] 必须是字符串"},
	}
	for _, tc := range cases {
		res := validateTemplateContent(tc.content)
		found := false
		for _, e := range res.Errors {
			if strings.Contains(e, tc.want) {
				found = true
				break
			}
		}
		if res.OK || !found {
			t.Fatalf("%s：期望错误包含 %q，得到 %+v", tc.name, tc.want, res.Errors)
		}
	}
}

func TestValidateTemplateContentWarnings(t *testing.T) {
	content := strings.Replace(templateEditorSample,
		"RULE-SET,domain-list,🚀 节点选择", "RULE-SET,missing-provider,不存在的策略", 1)
	res := validateTemplateContent(content)
	if !res.OK {
		t.Fatalf("引用问题应为 warning 而非 error：%+v", res.Errors)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "rule-provider 不存在") || !strings.Contains(joined, "目标策略不存在") {
		t.Fatalf("缺少引用警告：%v", res.Warnings)
	}
}

// 热修（task-22）：线上 lite 模板的 filter 含负向前瞻 `(?!...)`，Go regexp 不支持，
// 必须降级为 warning（客户端兼容性需确认），不能阻断发布。
func TestValidateTemplateContentLookaheadFilterWarning(t *testing.T) {
	content := strings.Replace(templateEditorSample,
		"tolerance: 50", "tolerance: 50\n    filter: \"^((?!(回国|中国|China|CN|Mainland)).)*$\"", 1)
	res := validateTemplateContent(content)
	if !res.OK || len(res.Errors) != 0 {
		t.Fatalf("负向前瞻 filter 不应阻断发布：errors=%v", res.Errors)
	}
	joined := strings.Join(res.Warnings, "\n")
	if !strings.Contains(joined, "filter 不是合法 Go 正则") || !strings.Contains(joined, "客户端兼容性需确认") {
		t.Fatalf("缺少 filter 兼容性 warning：%v", res.Warnings)
	}
}

// 热修（task-22）：规则解析不能误报——no-resolve 不是目标策略、AND/OR/NOT 不按
// rule-set 校验 provider、IP-ASN 是已知类型。
func TestValidateTemplateContentRuleNoFalseWarnings(t *testing.T) {
	oldRules := "rules:\n  - RULE-SET,domain-list,🚀 节点选择\n  - MATCH,🚀 节点选择"
	if !strings.Contains(templateEditorSample, oldRules) {
		t.Fatal("测试样例 rules 段落已变化，请同步更新用例")
	}
	newRules := `rules:
  - IP-CIDR,198.18.0.0/16,DIRECT,no-resolve
  - GEOIP,CN,DIRECT,no-resolve
  - IP-ASN,13335,DIRECT
  - AND,((NETWORK,UDP),(DOMAIN-SUFFIX,example.com)),🚀 节点选择
  - RULE-SET,domain-list,🚀 节点选择
  - MATCH,🚀 节点选择`
	content := strings.Replace(templateEditorSample, oldRules, newRules, 1)
	res := validateTemplateContent(content)
	if !res.OK {
		t.Fatalf("合法规则不应产生 error：%v", res.Errors)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("规则不应产生误报 warning（实际 %d 条）：%v", len(res.Warnings), res.Warnings)
	}
}

// 热修（task-22）：patch-section 后 summary.size_bytes 必须等于新内容字节数。
func TestPatchTemplateSectionSummarySizeBytes(t *testing.T) {
	content, sections, _, verrs, err := patchTemplateSection(templateEditorSample, "rules",
		json.RawMessage(`["IP-CIDR,198.18.0.0/16,DIRECT,no-resolve","MATCH,🚀 节点选择"]`))
	if err != nil || len(verrs) != 0 {
		t.Fatalf("patch 失败：%v %v", err, verrs)
	}
	if sections.Summary.SizeBytes != len(content) || sections.Summary.SizeBytes <= 0 {
		t.Fatalf("size_bytes = %d，期望 %d（内容长度）", sections.Summary.SizeBytes, len(content))
	}
}

// ---------------- 受管 R2 ----------------

func TestManagedTemplateObjectKey(t *testing.T) {
	hosts := []string{"s3.proxypilot.yangzifun.org", "cdn.example.com"}
	key, err := managedTemplateObjectKey("https://s3.proxypilot.yangzifun.org/rule/configs/global.yaml", hosts)
	if err != nil || key != "rule/configs/global.yaml" {
		t.Fatalf("合法 URL 解析 = %q err=%v", key, err)
	}
	key, err = managedTemplateObjectKey("https://cdn.example.com/rule/configs/a.yml", hosts)
	if err != nil || key != "rule/configs/a.yml" {
		t.Fatalf("CDN 域解析 = %q err=%v", key, err)
	}
	bad := []struct{ url, want string }{
		{"https://evil.example.com/rule/configs/a.yaml", "外部模板只读"},
		{"https://s3.proxypilot.yangzifun.org/other/a.yaml", "路径必须以"},
		{"https://s3.proxypilot.yangzifun.org/rule/configs/a.json", ".yaml/.yml"},
		{"https://s3.proxypilot.yangzifun.org/rule/configs/../a.yaml", "路径不合法"},
		{"not-a-url", "无效"},
	}
	for _, tc := range bad {
		if _, err := managedTemplateObjectKey(tc.url, hosts); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：期望错误含 %q，得到 %v", tc.url, tc.want, err)
		}
	}
}

type fakeTemplateStore struct {
	puts   map[string][]byte
	gets   map[string][]byte
	putErr error
}

func (s *fakeTemplateStore) Put(ctx context.Context, key string, data []byte, contentType string) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.puts == nil {
		s.puts = map[string][]byte{}
	}
	s.puts[key] = data
	return nil
}

func (s *fakeTemplateStore) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if s.gets == nil {
		return nil, false, nil
	}
	data, ok := s.gets[key]
	return data, ok, nil
}

func withTemplateStore(t *testing.T, store templateObjectStore) {
	t.Helper()
	orig := newTemplateObjectStore
	newTemplateObjectStore = func(cfg r2Config) templateObjectStore { return store }
	t.Cleanup(func() { newTemplateObjectStore = orig })
}

// ---------------- 运行时解析 ----------------

func TestResolveClashTemplateContent(t *testing.T) {
	origFetch := templateHTTPFetch
	defer func() { templateHTTPFetch = origFetch }()
	templateHTTPFetch = func(ctx context.Context, rawURL string) (string, error) {
		return "port: 7890 # from url\n", nil
	}
	clearTemplateURLCache()
	defer clearTemplateURLCache()

	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE name") {
			name := ""
			if len(args) > 0 {
				name, _ = args[0].(string)
			}
			if name == "a" {
				return fakeRowsSet([]string{"url"},
					[]driver.Value{"https://s3.proxypilot.yangzifun.org/rule/configs/a.yaml"}), nil
			}
			return fakeRowsSet([]string{"url"},
				[]driver.Value{"https://s3.proxypilot.yangzifun.org/rule/configs/b.yaml"}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}

	// 运行时以存储桶 URL 为唯一来源（不再优先 DB published_content）
	got, err := h.resolveClashTemplateContent(context.Background(), "a")
	if err != nil || got.Source != "url" || !strings.Contains(got.Content, "from url") {
		t.Fatalf("模板 a 应读取 URL：%+v err=%v", got, err)
	}
	got, err = h.resolveClashTemplateContent(context.Background(), "b")
	if err != nil || got.Source != "url" || !strings.Contains(got.Content, "from url") {
		t.Fatalf("URL 回退失败：%+v err=%v", got, err)
	}
	// 直接 URL 输入
	got, err = h.resolveClashTemplateContent(context.Background(), "https://example.com/t.yaml")
	if err != nil || got.Source != "url" || got.URL != "https://example.com/t.yaml" {
		t.Fatalf("直接 URL 解析失败：%+v err=%v", got, err)
	}
	// 模板不存在
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	if _, err := h.resolveClashTemplateContent(context.Background(), "missing"); !errors.Is(err, errTemplateNotFound) {
		t.Fatalf("不存在模板应返回 errTemplateNotFound：%v", err)
	}
}

// ---------------- 草稿乐观锁 ----------------

func templateEditorRow() []driver.Value {
	return []driver.Value{
		int64(7), "tpl.yaml", "https://s3.proxypilot.yangzifun.org/rule/configs/tpl.yaml", "",
		"port: 7890\n", "port: 1\n", "draft-sha", "pub-sha",
		int64(1000), "admin", int64(900), "admin", int64(3),
	}
}

func TestTemplateContentSaveOptimisticLock(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE id") {
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{
			R2Bucket: "proxypilot", R2Endpoint: "https://r2.example.com",
			R2AccessKeyID: "ak", R2SecretAccessKey: "sk",
		},
	}
	store := &fakeTemplateStore{gets: map[string][]byte{}}
	withTemplateStore(t, store)

	call := func(body string) (int, map[string]any) {
		req := httptest.NewRequest(http.MethodPut, "/api/templates/manage/7/content", strings.NewReader(body))
		req.SetPathValue("id", "7")
		rec := httptest.NewRecorder()
		h.templateContentSave(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	if code, out := call(`{"content":"port: 7890\n"}`); code != http.StatusOK {
		t.Fatalf("不带 base_sha 保存应成功，得到 %d body=%v", code, out)
	}
	if _, ok := store.puts["rule/configs/tpl.yaml"]; !ok {
		t.Fatal("内容未直写模板桶对象")
	}
	var updatedContent bool
	var versionInsert bool
	for _, call := range fdb.execCalls() {
		if strings.Contains(call.Query, "UPDATE config_templates") && strings.Contains(call.Query, "published_content") {
			for _, arg := range call.Args {
				if s, ok := arg.(string); ok && strings.Contains(s, "port: 7890") {
					updatedContent = true
				}
			}
		}
		if strings.Contains(call.Query, "INSERT INTO config_template_versions") {
			for _, arg := range call.Args {
				if n, ok := arg.(int64); ok && n == int64(4) {
					versionInsert = true
				}
			}
		}
	}
	if !updatedContent || !versionInsert {
		t.Fatalf("DB 未同步 published_content/版本：updated=%v version=%v", updatedContent, versionInsert)
	}
	if code, _ := call(`{"content":"port: 7890\n","base_sha256":"stale"}`); code != http.StatusConflict {
		t.Fatalf("base_sha 不匹配应 409，得到 %d", code)
	}
	if code, _ := call(`{"content":"a: [1\n","base_sha256":"draft-sha"}`); code != http.StatusBadRequest {
		t.Fatalf("非法 YAML 应 400，得到 %d", code)
	}
}

// ---------------- 发布 ----------------

func publishHandlers(t *testing.T) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE id") {
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{
		db:  db,
		log: testLogger(),
		cfg: config.Config{
			R2Bucket: "proxypilot", R2Endpoint: "https://r2.example.com",
			R2AccessKeyID: "ak", R2SecretAccessKey: "sk",
		},
	}
	return h, fdb
}

func TestTemplatePublishSuccess(t *testing.T) {
	h, fdb := publishHandlers(t)
	store := &fakeTemplateStore{gets: map[string][]byte{
		"rule/configs/tpl.yaml": []byte("# old template\n"),
	}}
	withTemplateStore(t, store)

	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/publish", strings.NewReader(`{}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templatePublish(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("发布失败：code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data templatePublishResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if out.Data.Version != 4 || out.Data.URL == "" || out.Data.SHA256 == "" {
		t.Fatalf("发布结果 = %+v", out.Data)
	}
	if _, ok := store.puts["rule/configs/tpl.yaml"]; !ok {
		t.Fatal("未写入新模板对象")
	}
	backupFound := false
	for key := range store.puts {
		if strings.HasPrefix(key, "rule/configs/.versions/") && strings.HasSuffix(key, ".yaml") {
			backupFound = true
		}
	}
	if !backupFound {
		t.Fatalf("未备份旧对象：%v", store.puts)
	}
	if fdb.countExec("INSERT INTO config_template_versions") != 1 {
		t.Fatal("未记录发布版本")
	}
	if fdb.countExec("UPDATE config_templates") != 1 {
		t.Fatal("未更新模板发布内容")
	}
	if fdb.countExec("INSERT INTO node_push_logs") != 1 {
		t.Fatal("缺少发布审计日志")
	}
}

func TestTemplatePublishRejects(t *testing.T) {
	// 外部 URL 只读
	h, fdb := publishHandlers(t)
	store := &fakeTemplateStore{}
	withTemplateStore(t, store)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE id") {
			row := templateEditorRow()
			row[2] = "https://evil.example.com/rule/configs/x.yaml"
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, row), nil
		}
		return &fakeRows{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/publish", strings.NewReader(`{}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templatePublish(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "外部模板只读") {
		t.Fatalf("外部模板应拒绝：code=%d body=%s", rec.Code, rec.Body.String())
	}

	// 校验失败
	h2, fdb2 := publishHandlers(t)
	fdb2.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE id") {
			row := templateEditorRow()
			row[4] = "proxies:\n  - {name: 我的节点, type: trojan}\n" // 非法 proxies
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, row), nil
		}
		return &fakeRows{}, nil
	}
	req = httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/publish", strings.NewReader(`{}`))
	req.SetPathValue("id", "7")
	rec = httptest.NewRecorder()
	h2.templatePublish(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "模板校验未通过") {
		t.Fatalf("校验失败应拒绝发布：code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTemplatePublishR2NotReady(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(query, "FROM config_templates WHERE id") {
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()} // 未配置 R2
	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/publish", strings.NewReader(`{}`))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templatePublish(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "R2 未配置") {
		t.Fatalf("R2 未配置应拒绝：code=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ---------------- 预览 ----------------

func TestTemplatePreviewInjectsProxies(t *testing.T) {
	origLinks := templatePreviewLinks
	defer func() { templatePreviewLinks = origLinks }()
	const uuid = "11111111-1111-4111-8111-111111111111"
	templatePreviewLinks = func(h *handlers, ctx context.Context, gotUUID string) ([]string, extSummary, error) {
		if gotUUID != uuid {
			t.Fatalf("预览 uuid = %q", gotUUID)
		}
		return []string{
			"vless://" + uuid + "@1.2.3.4:443?security=tls&type=ws&host=a.example.com&path=%2Fws&sni=a.example.com#node1",
			"trojan://pass@5.6.7.8:443?sni=b.example.com#node2",
		}, extSummary{}, nil
	}
	fdb, db := newFakeDB(t)
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/preview",
		strings.NewReader(`{"content":"port: 7890\n" + "", "uuid":"`+uuid+`"}`))
	// 上面字符串拼接在 JSON 里不合法，改用 fmt 构造
	body := `{"content":"port: 7890\n","uuid":"` + uuid + `"}`
	req = httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/preview", strings.NewReader(body))
	req.SetPathValue("id", "7")
	rec := httptest.NewRecorder()
	h.templatePreview(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("预览失败：code=%d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			YAML    string `json:"yaml"`
			Proxies int    `json:"proxies"`
			Total   int    `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if out.Data.Proxies < 1 || out.Data.Total != 2 {
		t.Fatalf("预览统计 = %+v", out.Data)
	}
	if !strings.Contains(out.Data.YAML, "proxies:") || !strings.Contains(out.Data.YAML, "port: 7890") {
		t.Fatalf("预览 YAML 未注入 proxies：\n%s", out.Data.YAML)
	}
	if fdb.countExec("config_access_logs") != 0 {
		t.Fatal("预览不应写访问日志")
	}
}

// ---------------- 版本恢复 ----------------

func TestTemplateVersionRestoreWritesDraft(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "FROM config_template_versions"):
			return fakeRowsSet([]string{"content"}, []driver.Value{"port: 7000\n"}), nil
		case strings.Contains(query, "FROM config_templates WHERE id"):
			return fakeRowsSet([]string{
				"id", "name", "url", "description", "draft_content", "published_content",
				"draft_sha256", "published_sha256", "draft_updated_at", "draft_updated_by",
				"published_at", "published_by", "content_version"}, templateEditorRow()), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/templates/manage/7/versions/3/restore", strings.NewReader(`{}`))
	req.SetPathValue("id", "7")
	req.SetPathValue("vid", "3")
	rec := httptest.NewRecorder()
	h.templateVersionRestore(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("恢复失败：code=%d body=%s", rec.Code, rec.Body.String())
	}
	call, ok := fdb.lastExec("UPDATE config_templates")
	if !ok {
		t.Fatal("恢复版本应写草稿")
	}
	found := false
	for _, arg := range call.Args {
		if s, ok := arg.(string); ok && strings.Contains(s, "port: 7000") {
			found = true
		}
	}
	if !found {
		t.Fatalf("恢复内容未写入草稿：%v", call.Args)
	}
	if fdb.countExec("INSERT INTO config_template_versions") != 1 {
		t.Fatal("恢复应记录版本")
	}
}
