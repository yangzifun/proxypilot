package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestSingboxRenderGolden 用 worker.js 生成的黄金用例比对 sb.json 与端口。
// 重新生成：node workers/gen_singbox_fixtures.mjs
func TestSingboxRenderGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "singbox", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("未找到黄金用例: %v", err)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fx struct {
				Template singboxTemplate  `json:"template"`
				Users    []sbUser         `json:"users"`
				Node     *singboxNode     `json:"node"`
				Ports    map[string]int   `json:"ports"`
				SB       map[string]any   `json:"sb"`
				Links    []map[string]any `json:"links"`
			}
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("解析用例失败: %v", err)
			}

			ports := singboxPorts(&fx.Template)
			if !reflect.DeepEqual(ports, fx.Ports) {
				t.Fatalf("端口不一致\n got: %v\nwant: %v", ports, fx.Ports)
			}

			users := singboxParseUsers(fx.Users)
			got := singboxRenderSb(&fx.Template, users, ports, "", false, "")
			gotJSON := canonicalJSON(got)
			wantJSON := canonicalJSON(fx.SB)
			if gotJSON != wantJSON {
				t.Fatalf("sb.json 不一致\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestSingboxPortsDedup(t *testing.T) {
	tpl := &singboxTemplate{
		PortBase: 13700, WarpBase: 13800,
		ProtocolOptions: `{"ss":{"port":13701}}`, // 与 hy2 撞车 → 应回落到 13702
	}
	ports := singboxPorts(tpl)
	if ports["ss"] == ports["hy2"] {
		t.Fatalf("ss/hy2 仍冲突: %v", ports)
	}
	if ports["ss"] != 13702 {
		t.Fatalf("ss = %d，期望 13702", ports["ss"])
	}
	if ports["ss_warp"] != 13802 {
		t.Fatalf("ss_warp = %d", ports["ss_warp"])
	}
}

func TestSingboxParseUsers(t *testing.T) {
	users := singboxParseUsers([]sbUser{
		{Name: "", UUID: " u-1 ", SSPassword: " p "},
		{Name: "bob", UUID: ""},
	})
	if len(users) != 1 {
		t.Fatalf("users = %#v", users)
	}
	if users[0].Name != "user1" || users[0].UUID != "u-1" || users[0].SSPassword != "p" {
		t.Fatalf("user = %#v", users[0])
	}
}

// TestCDNListenRender 覆盖 vless-ws-cdn 的 listen 白名单：
// 旧模板缺省 127.0.0.1；0.0.0.0/:: 允许；[::] 归一化为 ::；非法值回退 127。
func TestCDNListenRender(t *testing.T) {
	users := []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}}
	cases := []struct{ name, options, want string }{
		{"缺省", `{"vlws":{"cdn_port":13707}}`, "127.0.0.1"},
		{"回环", `{"vlws":{"cdn_port":13707,"cdn_listen":"127.0.0.1"}}`, "127.0.0.1"},
		{"公网IPv4", `{"vlws":{"cdn_port":13707,"cdn_listen":"0.0.0.0"}}`, "0.0.0.0"},
		{"公网IPv6", `{"vlws":{"cdn_port":13707,"cdn_listen":"::"}}`, "::"},
		{"IPv6方括号", `{"vlws":{"cdn_port":13707,"cdn_listen":"[::]"}}`, "::"},
		{"非法值", `{"vlws":{"cdn_port":13707,"cdn_listen":"1.2.3.4"}}`, "127.0.0.1"},
		{"非IP字符串", `{"vlws":{"cdn_port":13707,"cdn_listen":"localhost"}}`, "127.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tpl := &singboxTemplate{
				ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1,
				CertPath: "/etc/sing-box/cert.crt", KeyPath: "/etc/sing-box/private.key",
				ProtocolOptions: tc.options,
			}
			cfg := singboxRenderSb(tpl, users, singboxPorts(tpl), "", false, "cdn.example.com")
			if got := cdnInboundListen(t, cfg); got != tc.want {
				t.Fatalf("listen = %q，期望 %q", got, tc.want)
			}
		})
	}
}

func cdnInboundListen(t *testing.T, cfg map[string]any) string {
	t.Helper()
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		ib, _ := raw.(map[string]any)
		if ib["tag"] == "vless-ws-cdn" {
			return fmt.Sprint(ib["listen"])
		}
	}
	t.Fatal("未生成 vless-ws-cdn inbound")
	return ""
}
