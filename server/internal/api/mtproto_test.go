package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMtprotoRenderGolden 用 worker.js 生成的黄金用例比对 effective / config / link。
// 重新生成：node workers/gen_mtproto_fixtures.mjs
func TestMtprotoRenderGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "mtproto", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("未找到 MTProto 黄金用例: %v", err)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fx struct {
				Template  mtprotoTemplate `json:"template"`
				Node      mtprotoNode     `json:"node"`
				Clients   []mtprotoClient `json:"clients"`
				Server    mtprotoServer   `json:"server"`
				Effective map[string]any  `json:"effective"`
				Config    struct {
					OK      bool             `json:"ok"`
					Error   string           `json:"error"`
					Content string           `json:"content"`
					Ports   map[string]int64 `json:"ports"`
					Mode    string           `json:"mode"`
				} `json:"config"`
				Link map[string]any `json:"link"`
			}
			if err := json.Unmarshal(raw, &fx); err != nil {
				t.Fatalf("解析用例失败: %v", err)
			}

			eff := mtprotoEffective(&fx.Template, &fx.Node)
			if got, want := canonicalAny(eff), canonicalJSON(fx.Effective); got != want {
				t.Fatalf("effective 不一致\n got: %s\nwant: %s", got, want)
			}

			content, ports, mode, msg := mtprotoRenderConfig(&eff, fx.Clients)
			if fx.Config.OK && msg != "" {
				t.Fatalf("config 渲染失败: %s", msg)
			}
			if content != fx.Config.Content {
				t.Fatalf("config 内容不一致\n got: %q\nwant: %q", content, fx.Config.Content)
			}
			if mode != fx.Config.Mode {
				t.Fatalf("mode = %q，期望 %q", mode, fx.Config.Mode)
			}
			for k, v := range fx.Config.Ports {
				if ports[k] != v {
					t.Fatalf("port[%s] = %d，期望 %d", k, ports[k], v)
				}
			}

			active := mtprotoActiveClients(fx.Clients)
			var first *mtprotoClient
			if len(active) > 0 {
				first = &active[0]
			}
			link := mtprotoClientLink(&eff, fx.Server, first)
			if got, want := canonicalAny(link), canonicalJSON(fx.Link); got != want {
				t.Fatalf("link 不一致\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func canonicalAny(v any) string {
	b, _ := json.Marshal(v)
	var x any
	_ = json.Unmarshal(b, &x)
	return canonicalJSON(x)
}

func TestMtprotoSecret(t *testing.T) {
	secret := mtprotoGenerateSecret("storage.googleapis.com")
	if !mtprotoSecretValid(secret) {
		t.Fatalf("生成的 secret 无效: %s", secret)
	}
	if got := mtprotoSecretDomain(secret); got != "storage.googleapis.com" {
		t.Fatalf("secret domain = %q", got)
	}
	if mtprotoSecretValid("ee00") {
		t.Fatal("过短 secret 应无效")
	}
	if mtprotoDomainOf("1.2.3.4") != "" {
		t.Fatal("IP 不能作为伪装域名")
	}
	if mtprotoDomainOf("storage.googleapis.com") == "" {
		t.Fatal("合法域名被拒绝")
	}
}

func TestMtprotoConfigValidation(t *testing.T) {
	tpl := &mtprotoTemplate{Name: "t", Domain: "storage.googleapis.com", Port: 13709, APIPort: 13719}
	node := &mtprotoNode{Name: "n", BootstrapSecret: mtprotoGenerateSecret("storage.googleapis.com")}
	eff := mtprotoEffective(tpl, node)
	// 统计端口与代理端口相同
	eff.APIPort = 13709
	if _, _, _, msg := mtprotoRenderConfig(&eff, nil); msg == "" {
		t.Fatal("端口冲突应报错")
	}
	// 无客户端且 bootstrap 无效
	eff.APIPort = 13719
	eff.BootstrapSecret = "bad"
	if _, _, _, msg := mtprotoRenderConfig(&eff, nil); msg == "" {
		t.Fatal("无可用 secret 应报错")
	}
}
