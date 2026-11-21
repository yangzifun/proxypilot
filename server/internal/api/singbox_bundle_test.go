package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSingboxRenderBundleValidation(t *testing.T) {
	base := &singboxTemplate{
		Name: "t", PortBase: 13700, WarpBase: 13800,
		UsersJSON: `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`,
	}
	node := singboxRenderNode{Name: "n1", Domain: "example.com"}

	// vless-ws 缺域名
	tpl := *base
	tpl.EnableVLWS = 1
	if _, _, _, msg := singboxRenderBundle(&tpl, singboxRenderNode{Name: "n1"}, serverRow{}, nil); msg == "" {
		t.Fatal("启用 vlws 但无域名应报错")
	}

	// 占位 UUID
	demo := *base
	demo.UsersJSON = `[{"name":"u","uuid":"00000000-0000-4000-8000-000000000000","ss_password":"p"}]`
	if _, _, _, msg := singboxRenderBundle(&demo, node, serverRow{}, nil); msg == "" {
		t.Fatal("占位 UUID 应报错")
	}

	// 正常：仅 sing-box → 1 份 sb.json
	files, ports, users, msg := singboxRenderBundle(&tpl, node, serverRow{}, nil)
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if len(files) != 1 || files[0]["key"] != "sb" {
		t.Fatalf("files = %#v", files)
	}
	if ports["ss"] != 13702 {
		t.Fatalf("ports = %#v", ports)
	}
	if len(users) != 1 || users[0]["uuid"] != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("users = %#v", users)
	}
}

func TestSingboxUserStatsCapableFor(t *testing.T) {
	cases := []struct {
		name, ver, base string
		want            bool
	}{
		{"自建版本后缀", "1.14.4-proxypilot", "", true},
		{"二进制标签", "unknown-with_v2ray_api", "", true},
		{"显式版本+组件基址", "1.14.4", "https://cdn/singbox", true},
		{"官方版本无基址", "1.14.4", "", false},
		{"未知版本+组件基址", "unknown", "https://cdn/singbox", true},
		{"空版本+组件基址", "", "https://cdn/singbox", true},
		{"未知版本无基址", "unknown", "", false},
		{"空版本无基址", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := singboxUserStatsCapableFor(tc.ver, tc.base); got != tc.want {
				t.Fatalf("singboxUserStatsCapableFor(%q,%q)=%v，期望 %v", tc.ver, tc.base, got, tc.want)
			}
		})
	}
}

// 自建 sing-box 的 version 输出可能是 unknown，但 component_base_url 指向面板组件；
// 此时必须仍写入 experimental.v2ray_api，否则 v2ray user stats 采集不到，用户流量页会全部离线。
func TestSingboxRenderBundleUserStatsUsesCustomComponentBase(t *testing.T) {
	tpl := &singboxTemplate{
		Name: "t", PortBase: 13700, WarpBase: 13800, EnableVLWS: 1,
		UsersJSON: `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`,
	}
	node := singboxRenderNode{Name: "n1", Domain: "example.com", StatsSecret: "stats-secret"}
	files, _, _, msg := singboxRenderBundle(tpl, node, serverRow{ComponentBase: "https://cdn/singbox"}, nil)
	if msg != "" || len(files) != 1 {
		t.Fatalf("files=%d msg=%s", len(files), msg)
	}
	content, _ := files[0]["content"].(string)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(content), &cfg); err != nil {
		t.Fatalf("sb.json 非法: %v", err)
	}
	exp, _ := cfg["experimental"].(map[string]any)
	if _, ok := exp["v2ray_api"]; !ok {
		t.Fatalf("自建组件应包含 experimental.v2ray_api：%s", content)
	}
	if !strings.Contains(content, `"v2ray_api"`) {
		t.Fatalf("配置缺少 v2ray_api：%s", content)
	}

	// 无组件基址（官方/未知环境）保持原安全行为，不写 v2ray_api。
	files, _, _, msg = singboxRenderBundle(tpl, node, serverRow{}, nil)
	if msg != "" || len(files) != 1 {
		t.Fatalf("files=%d msg=%s", len(files), msg)
	}
	content, _ = files[0]["content"].(string)
	if strings.Contains(content, `"v2ray_api"`) {
		t.Fatalf("无组件基址时不应写 v2ray_api：%s", content)
	}
}
