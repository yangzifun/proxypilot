package api

import (
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCDNPreferredExpandsLinks(t *testing.T) {
	tpl := &singboxTemplate{
		ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1,
		CertPath: "/etc/sing-box/cert.crt", KeyPath: "/etc/sing-box/private.key",
		ProtocolOptions: `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":1,"group":"cloudflare","mode":"expand"}}}`,
	}
	cdnPreferMu.Lock()
	old := cdnPreferCache
	cdnPreferCache = []cdnPrefer{
		{Address: "162.159.198.1", Port: 443, Group: "cloudflare", Enabled: 1},
		{Address: "yg1.ygkkk.dpdns.org", Port: 443, Group: "cloudflare", Enabled: 1},
	}
	cdnPreferMu.Unlock()
	defer func() {
		cdnPreferMu.Lock()
		cdnPreferCache = old
		cdnPreferMu.Unlock()
	}()

	node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNDomain: "cdn.example.com"}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})

	cdnLinks := []map[string]any{}
	for _, l := range links {
		if b, _ := l["cdn"].(bool); b {
			cdnLinks = append(cdnLinks, l)
		}
	}
	if len(cdnLinks) != 2 {
		t.Fatalf("期望 2 条 CDN 优选链接，实际 %d", len(cdnLinks))
	}
	joined := ""
	for _, l := range cdnLinks {
		s, _ := l["link"].(string)
		joined += s + "\n"
	}
	for _, want := range []string{"162.159.198.1:443", "yg1.ygkkk.dpdns.org:443"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少优选地址 %s:\n%s", want, joined)
		}
	}
	// SNI 与 Host 必须保持节点 CDN 域名
	if strings.Count(joined, "sni=cdn.example.com") != 2 || strings.Count(joined, "host=cdn.example.com") != 2 {
		t.Fatalf("SNI/Host 未保持 CDN 域名:\n%s", joined)
	}
	// 关闭优选时应回退为 cdn.example.com 单条
	tpl.ProtocolOptions = `{"vlws":{"path":"/vlws"}}`
	links = singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	found := false
	for _, l := range links {
		if s, _ := l["link"].(string); strings.Contains(s, "@cdn.example.com:443") {
			found = true
		}
	}
	if !found {
		t.Fatal("未启用优选时应回退 cdn.example.com")
	}
}

func TestNormalizeCDNPreferredGroup(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"", cdnPreferredDefaultGroup},
		{"   ", cdnPreferredDefaultGroup},
		{"default", cdnPreferredDefaultGroup},
		{" DEFAULT ", cdnPreferredDefaultGroup},
		{"cloudflare", "cloudflare"},
		{" CloudFlare ", "CloudFlare"}, // 非 default 的自定义名不强制小写，原样保留
		{"aliyun", "aliyun"},
		{" Tencent ", "Tencent"},
		{"overseas", "overseas"},
	}
	for _, tc := range cases {
		if got := normalizeCDNPreferredGroup(tc.raw); got != tc.want {
			t.Fatalf("normalizeCDNPreferredGroup(%q) = %q，期望 %q", tc.raw, got, tc.want)
		}
	}
}

// 旧配置兼容：模板/库里显式 group=default 都要映射到 cloudflare。
func TestCDNPreferredForGroupLegacyDefaultCompat(t *testing.T) {
	cdnPreferMu.Lock()
	old := cdnPreferCache
	cdnPreferCache = []cdnPrefer{
		{ID: 1, Address: "162.159.198.1", Port: 443, Group: "cloudflare", Enabled: 1},
		{ID: 2, Address: "162.159.198.2", Port: 443, Group: "default", Enabled: 1}, // 迁移前遗留行
		{ID: 3, Address: "1.1.1.1", Port: 443, Group: "aliyun", Enabled: 1},
	}
	cdnPreferMu.Unlock()
	defer func() {
		cdnPreferMu.Lock()
		cdnPreferCache = old
		cdnPreferMu.Unlock()
	}()

	for _, group := range []string{"", "default", " DEFAULT ", "cloudflare"} {
		if got := cdnPreferredForGroup(group); len(got) != 2 {
			t.Fatalf("group=%q 期望 2 条 cloudflare（含遗留 default），实际 %d", group, len(got))
		}
	}
	if got := cdnPreferredForGroup("aliyun"); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("aliyun 分组 = %v", got)
	}
	if got := cdnPreferredForGroup("tencent"); len(got) != 0 {
		t.Fatalf("tencent 分组应为空，实际 %v", got)
	}
}

// ReloadCDNPreferred 载入时把遗留 default 行归一化为 cloudflare（迁移前也能正确展开）。
func TestReloadCDNPreferredNormalizesLegacyDefault(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(query, "FROM cdn_preferred_addresses"):
			return fakeRowsSet([]string{"id", "group_name", "address", "port", "remark", "enabled", "sort"},
				[]driver.Value{int64(1), "default", "162.159.198.1", int64(443), "", int64(1), int64(0)},
				[]driver.Value{int64(2), "cloudflare", "162.159.198.2", int64(443), "", int64(1), int64(0)},
				[]driver.Value{int64(3), "aliyun", "1.1.1.1", int64(443), "", int64(1), int64(0)},
			), nil
		case strings.Contains(query, "cdn_prefer_enabled"):
			return fakeRowsSet([]string{"value"}, []driver.Value{"1"}), nil
		}
		return &fakeRows{}, nil
	}
	cdnPreferMu.Lock()
	oldCache, oldOn := cdnPreferCache, cdnPreferGlobalOn
	cdnPreferMu.Unlock()
	defer func() {
		cdnPreferMu.Lock()
		cdnPreferCache, cdnPreferGlobalOn = oldCache, oldOn
		cdnPreferMu.Unlock()
	}()

	ReloadCDNPreferred(t.Context(), db)
	if !cdnPreferEnabled() {
		t.Fatal("全局开关应载入为 true")
	}
	if got := cdnPreferredForGroup(""); len(got) != 2 {
		t.Fatalf("cloudflare 分组应含遗留 default 行（2 条），实际 %d", len(got))
	}
	for _, p := range cdnPreferredForGroup("cloudflare") {
		if p.Group != cdnPreferredDefaultGroup {
			t.Fatalf("缓存中的分组未归一化：%+v", p)
		}
	}
	if got := cdnPreferredForGroup("aliyun"); len(got) != 1 {
		t.Fatalf("aliyun 分组 = %v", got)
	}
}

// Create：group 空/ default 写 cloudflare，其它自定义分组原样保留。
func TestCDNPreferredCreateGroupNormalization(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}

	cases := []struct{ group, want string }{
		{"", "cloudflare"},
		{"default", "cloudflare"},
		{"  DEFAULT  ", "cloudflare"},
		{"cloudflare", "cloudflare"},
		{"aliyun", "aliyun"},
		{" Tencent ", "Tencent"},
	}
	for _, tc := range cases {
		body := `{"address":"162.159.198.1","group_name":"` + tc.group + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/cdn-preferred", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.cdnPreferredCreate(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("group=%q 创建失败：code=%d body=%s", tc.group, rec.Code, rec.Body.String())
		}
		call, ok := fdb.lastExec("INSERT INTO cdn_preferred_addresses")
		if !ok {
			t.Fatalf("group=%q 未执行 INSERT", tc.group)
		}
		if got, _ := call.Args[0].(string); got != tc.want {
			t.Fatalf("group=%q 落库分组 = %q，期望 %q", tc.group, got, tc.want)
		}
	}
}

// Update：group 同样归一化，自定义分组不被改写。
func TestCDNPreferredUpdateGroupNormalization(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}

	cases := []struct{ group, want string }{
		{"default", "cloudflare"},
		{"", "cloudflare"},
		{"aliyun", "aliyun"},
	}
	for _, tc := range cases {
		body := `{"group_name":"` + tc.group + `"}`
		req := httptest.NewRequest(http.MethodPut, "/api/cdn-preferred/5", strings.NewReader(body))
		req.SetPathValue("id", "5")
		rec := httptest.NewRecorder()
		h.cdnPreferredUpdate(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("group=%q 更新失败：code=%d body=%s", tc.group, rec.Code, rec.Body.String())
		}
		call, ok := fdb.lastExec("UPDATE cdn_preferred_addresses SET")
		if !ok {
			t.Fatalf("group=%q 未执行 UPDATE", tc.group)
		}
		if got, _ := call.Args[0].(string); got != tc.want {
			t.Fatalf("group=%q 更新分组 = %q，期望 %q", tc.group, got, tc.want)
		}
	}
}

// Import：默认分组写 cloudflare，自定义分组原样保留。
func TestCDNPreferredImportGroupNormalization(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	fdb.onExec = func(query string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	h := &handlers{db: db, log: testLogger()}

	for _, tc := range []struct{ group, want string }{
		{"", "cloudflare"},
		{"default", "cloudflare"},
		{"overseas", "overseas"},
	} {
		body := `{"group_name":"` + tc.group + `","text":"162.159.198.1:443#test"}`
		req := httptest.NewRequest(http.MethodPost, "/api/cdn-preferred/import", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.cdnPreferredImport(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("group=%q 导入失败：code=%d body=%s", tc.group, rec.Code, rec.Body.String())
		}
		call, ok := fdb.lastExec("INSERT INTO cdn_preferred_addresses")
		if !ok {
			t.Fatalf("group=%q 未执行 INSERT", tc.group)
		}
		if got, _ := call.Args[0].(string); got != tc.want {
			t.Fatalf("group=%q 导入分组 = %q，期望 %q", tc.group, got, tc.want)
		}
	}
}

// 列表过滤：cloudflare 同时匹配遗留 default；自定义分组精确匹配。
func TestCDNPreferredListGroupFilter(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(query string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	h := &handlers{db: db, log: testLogger()}

	callList := func(query string) fakeSQLCall {
		req := httptest.NewRequest(http.MethodGet, "/api/cdn-preferred"+query, nil)
		rec := httptest.NewRecorder()
		h.cdnPreferredList(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("列表查询失败：code=%d", rec.Code)
		}
		calls := fdb.queryCalls()
		for i := len(calls) - 1; i >= 0; i-- {
			if strings.Contains(calls[i].Query, "FROM cdn_preferred_addresses") {
				return calls[i]
			}
		}
		t.Fatalf("未找到列表查询：%v", calls)
		return fakeSQLCall{}
	}

	call := callList("?group=default")
	if !strings.Contains(call.Query, "IN (?, 'default')") {
		t.Fatalf("default 过滤应兼容遗留行：%s", call.Query)
	}
	if len(call.Args) != 1 || call.Args[0] != "cloudflare" {
		t.Fatalf("default 过滤参数 = %#v", call.Args)
	}

	call = callList("?group=aliyun")
	if strings.Contains(call.Query, "IN (?, 'default')") {
		t.Fatalf("自定义分组不应匹配 default：%s", call.Query)
	}
	if len(call.Args) != 1 || call.Args[0] != "aliyun" {
		t.Fatalf("aliyun 过滤参数 = %#v", call.Args)
	}

	call = callList("")
	if len(call.Args) != 0 || strings.Contains(call.Query, "WHERE") {
		t.Fatalf("无分组时不应加 WHERE：%s %#v", call.Query, call.Args)
	}
}

func TestExpandStoredCDNLinks(t *testing.T) {
	cdnPreferMu.Lock()
	oldCache, oldOn := cdnPreferCache, cdnPreferGlobalOn
	cdnPreferCache = []cdnPrefer{
		{Address: "162.159.198.1", Port: 443, Group: "cloudflare", Enabled: 1},
		{Address: "yg1.ygkkk.dpdns.org", Port: 443, Group: "cloudflare", Enabled: 1},
	}
	cdnPreferGlobalOn = true
	cdnPreferMu.Unlock()
	defer func() {
		cdnPreferMu.Lock()
		cdnPreferCache, cdnPreferGlobalOn = oldCache, oldOn
		cdnPreferMu.Unlock()
	}()

	in := []string{
		"vless://11111111-1111-4111-8111-111111111111@cdn.example.com:443/?security=tls&sni=cdn.example.com&host=cdn.example.com&type=ws#%F0%9F%87%BA%F0%9F%87%B8%E6%B4%9B%E6%9D%89%E7%9F%B6-vlws-cdn",
		"vless://11111111-1111-4111-8111-111111111111@direct.example.com:8443/?security=tls#direct-vlws",
	}
	out := expandStoredCDNLinks(in)
	if len(out) != 3 {
		t.Fatalf("期望 3 条（2 优选 + 1 直连），实际 %d", len(out))
	}
	joined := strings.Join(out, "\\n")
	for _, want := range []string{"@162.159.198.1:443/?", "yg1.ygkkk.dpdns.org:443/?", "-cdn-1", "-cdn-2", "sni=cdn.example.com"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少 %q:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, "@direct.example.com:8443") {
		t.Fatal("直连链接不应被改写")
	}
}
