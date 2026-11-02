package api

import (
	"encoding/json"
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
			got := singboxRenderSb(&fx.Template, users, ports, "", false)
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
