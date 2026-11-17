package api

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// TestMtprotoLinkHostPriority 覆盖 task-40 的主机选择顺序：
// 配置域名 > 节点 Host（必须是域名）> 服务器 LastIP4/LastIP > srv.Host。
func TestMtprotoLinkHostPriority(t *testing.T) {
	srv := mtprotoServer{LastIP4: "203.0.113.7", LastIP: "198.51.100.9", Host: "server.example.net"}
	cases := []struct {
		name string
		eff  mtprotoEffectiveConfig
		srv  mtprotoServer
		want string
	}{
		{"已配置域名时不用服务器 IP", mtprotoEffectiveConfig{Domain: "storage.googleapis.com", Host: "203.0.113.7"}, srv, "storage.googleapis.com"},
		{"连接地址为域名时优先于伪装域名", mtprotoEffectiveConfig{Domain: "www.bing.com", Host: "hk1.pandajun.xyz"}, srv, "hk1.pandajun.xyz"},
		{"配置域名大小写/空格规范化", mtprotoEffectiveConfig{Domain: " Storage.GoogleAPIs.com "}, srv, "storage.googleapis.com"},
		{"无域名时用 Host 域名", mtprotoEffectiveConfig{Host: "front.example.com"}, srv, "front.example.com"},
		{"Host 是 IP 时忽略并回落到 LastIP4", mtprotoEffectiveConfig{Host: "203.0.113.7"}, srv, "203.0.113.7"},
		{"无 LastIP4 时回落到 LastIP", mtprotoEffectiveConfig{Host: ""}, mtprotoServer{LastIP: "198.51.100.9"}, "198.51.100.9"},
		{"全部为空时回落到 srv.Host", mtprotoEffectiveConfig{}, mtprotoServer{Host: "edge.example.org"}, "edge.example.org"},
		{"非法 Domain（IP）不误用", mtprotoEffectiveConfig{Domain: "1.2.3.4", Host: "front.example.com"}, srv, "front.example.com"},
		{"非法 Domain（含非法字符）不误用", mtprotoEffectiveConfig{Domain: "bad_domain", Host: ""}, srv, "203.0.113.7"},
		{"LastIP4 为 unknown 时继续用 LastIP", mtprotoEffectiveConfig{}, mtprotoServer{LastIP4: "unknown", LastIP: "198.51.100.9"}, "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mtprotoLinkHost(&tc.eff, tc.srv); got != tc.want {
				t.Fatalf("link host = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestMtprotoClientLinkUsesDomain 验证 tg:// 与 t.me 链接都使用配置域名，
// 且 domain 不会被服务器探测 IP 覆盖。
func TestMtprotoClientLinkUsesDomain(t *testing.T) {
	secret := mtprotoGenerateSecret("storage.googleapis.com")
	if secret == "" {
		t.Fatal("生成 secret 失败")
	}
	eff := mtprotoEffectiveConfig{
		Domain: "storage.googleapis.com", Host: "203.0.113.7",
		Port: 13709, SecretMode: "multi",
	}
	link := mtprotoClientLink(&eff, mtprotoServer{LastIP4: "203.0.113.7", LastIP: "203.0.113.7", Host: "203.0.113.7"},
		&mtprotoClient{ID: 1, Secret: secret})
	if got := mapStr(link, "host", ""); got != "storage.googleapis.com" {
		t.Fatalf("link.host = %q，期望 storage.googleapis.com", got)
	}
	for _, key := range []string{"tg", "tme"} {
		raw := mapStr(link, key, "")
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s 链接非法: %v", key, err)
		}
		if got := u.Query().Get("server"); got != "storage.googleapis.com" {
			t.Fatalf("%s server = %q，期望域名", key, got)
		}
		if got := u.Query().Get("secret"); got != secret {
			t.Fatalf("%s secret 不匹配", key)
		}
		if got := u.Query().Get("port"); got != "13709" {
			t.Fatalf("%s port = %q", key, got)
		}
	}
	if !strings.Contains(mapStr(link, "tg", ""), "server=storage.googleapis.com") ||
		!strings.Contains(mapStr(link, "tme", ""), "server=storage.googleapis.com") {
		t.Fatalf("tg/tme 链接未使用域名：%v / %v", link["tg"], link["tme"])
	}
}
