package api

import "testing"

func TestSingboxBool(t *testing.T) {
	cases := []struct {
		in   any
		dflt bool
		want int64
	}{
		{nil, true, 1}, {nil, false, 0},
		{true, false, 1}, {false, true, 0},
		{"1", false, 1}, {"true", false, 1}, {"on", false, 1},
		{"0", true, 0}, {"", true, 1}, {"", false, 0},
		{float64(1), false, 1}, {float64(2), false, 1},
	}
	for _, c := range cases {
		if got := singboxBool(c.in, c.dflt); got != c.want {
			t.Errorf("singboxBool(%v,%v) = %d，期望 %d", c.in, c.dflt, got, c.want)
		}
	}
}

func TestNormalizeDomain(t *testing.T) {
	ok := map[string]string{
		"cdn.example.com":           "cdn.example.com",
		"https://cdn.example.com/x": "cdn.example.com",
		"http://a.b/path":           "a.b",
	}
	for in, want := range ok {
		if got := normalizeDomain(in); got != want {
			t.Errorf("normalizeDomain(%q) = %q，期望 %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "-bad.com", "a b.com", "http://", "x/y"} {
		if got := normalizeDomain(bad); got != "" {
			t.Errorf("normalizeDomain(%q) 应为空，得到 %q", bad, got)
		}
	}
}

func TestNormalizeSingboxTemplateInput(t *testing.T) {
	_, msg := normalizeSingboxTemplateInput(map[string]any{"name": "t"})
	if msg == "" {
		t.Fatal("缺少 users 应报错")
	}

	in, msg := normalizeSingboxTemplateInput(map[string]any{
		"name":       "t1",
		"users_json": `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`,
	})
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if in["name"] != "t1" {
		t.Fatalf("name = %v", in["name"])
	}
	ssKey, _ := in["ss_server_key"].(string)
	if raw, ok := decodeAnyBase64(ssKey); !ok || len(raw) != 32 {
		t.Fatalf("ss_server_key 未生成 32 字节密钥: %q", ssKey)
	}
	realityKey, _ := in["reality_private_key"].(string)
	if len(realityKey) != 43 {
		t.Fatalf("reality_private_key 应 43 字符: %q", realityKey)
	}
	if pub, _ := in["reality_public_key"].(string); pub == "" {
		t.Fatal("reality_public_key 应自动推导")
	}
	if in["enable_ss"].(int64) != 1 {
		t.Fatal("enable_ss 默认应为 1")
	}
	if in["port_base"].(int64) != 13700 {
		t.Fatalf("port_base = %v", in["port_base"])
	}
}

func TestNormalizeSingboxNodeInput(t *testing.T) {
	_, msg := normalizeSingboxNodeInput(map[string]any{"name": "n"})
	if msg == "" {
		t.Fatal("缺少 server_id 应报错")
	}
	in, msg := normalizeSingboxNodeInput(map[string]any{
		"name": "n1", "server_id": float64(2), "cdn_domain": "https://cdn.example.com/x",
		"warp_enabled": float64(-1),
	})
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if in["cdn_domain"] != "cdn.example.com" {
		t.Fatalf("cdn_domain = %v", in["cdn_domain"])
	}
	if in["warp_enabled"].(int64) != -1 {
		t.Fatalf("warp_enabled = %v", in["warp_enabled"])
	}
	if in["sb_path"] != "/etc/sing-box/sb.json" {
		t.Fatalf("sb_path = %v", in["sb_path"])
	}
}
