package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSingboxStatsPorts(t *testing.T) {
	clash, v2ray := singboxStatsPorts(map[string]int{"base": 13700, "ss": 13702, "reality": 13705})
	if clash != 13790 || v2ray != 13791 {
		t.Fatalf("默认统计端口 = %d/%d，期望 13790/13791", clash, v2ray)
	}
	// 与协议端口撞车时顺延
	clash2, v2ray2 := singboxStatsPorts(map[string]int{"base": 13700, "ss": 13790})
	if clash2 == 13790 {
		t.Fatalf("clash 端口应与 ss 冲突后顺延：%d", clash2)
	}
	if clash2 == v2ray2 {
		t.Fatalf("clash / v2ray 端口不能相同：%d/%d", clash2, v2ray2)
	}
}

func TestSingboxRenderStatsExperimental(t *testing.T) {
	on := int64(1)
	tpl := &singboxTemplate{
		Name: "t", PortBase: 13700, WarpBase: 13800,
		UsersJSON: `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`,
		EnableSS:  1, EnableSingbox: &on,
	}
	users := singboxParseUsers(currentTemplateUsers(tpl))
	ports := singboxPorts(tpl)

	cfg := singboxRenderSb(tpl, users, ports, "secret-abc", true, "")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Experimental *struct {
			ClashAPI struct {
				ExternalController string `json:"external_controller"`
				Secret             string `json:"secret"`
			} `json:"clash_api"`
			V2RayAPI struct {
				Listen string `json:"listen"`
				Stats  struct {
					Enabled bool     `json:"enabled"`
					Users   []string `json:"users"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
		Inbounds []struct {
			Users []struct {
				Name string `json:"name"`
			} `json:"users"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Experimental == nil {
		t.Fatal("启用统计后应写入 experimental")
	}
	if !strings.HasPrefix(doc.Experimental.ClashAPI.ExternalController, "127.0.0.1:") {
		t.Fatalf("clash_api 应只绑 127.0.0.1：%q", doc.Experimental.ClashAPI.ExternalController)
	}
	if doc.Experimental.ClashAPI.Secret != "secret-abc" {
		t.Fatalf("secret = %q", doc.Experimental.ClashAPI.Secret)
	}
	if !doc.Experimental.V2RayAPI.Stats.Enabled || len(doc.Experimental.V2RayAPI.Stats.Users) != 1 {
		t.Fatalf("v2ray stats = %+v", doc.Experimental.V2RayAPI.Stats)
	}
	// 统计开启时入站用户名切成 UUID，保证与 stats.users 一致
	if len(doc.Inbounds) == 0 || len(doc.Inbounds[0].Users) == 0 {
		t.Fatal("缺少入站用户")
	}
	want := "11111111-2222-3333-4444-555555555555"
	if doc.Inbounds[0].Users[0].Name != want || doc.Experimental.V2RayAPI.Stats.Users[0] != want {
		t.Fatalf("用户名未切成 UUID：inbound=%q stats=%v",
			doc.Inbounds[0].Users[0].Name, doc.Experimental.V2RayAPI.Stats.Users)
	}

	// 不传 secret → 不写 experimental（黄金用例口径）
	plain, _ := json.Marshal(singboxRenderSb(tpl, users, ports, "", false, ""))
	if strings.Contains(string(plain), `"experimental"`) {
		t.Fatal("未启用统计时不应写入 experimental")
	}
}

func TestStatsRangeStart(t *testing.T) {
	now := nowMillis()
	if v := statsRangeStart("24h"); now-v < 23*3600_000 || now-v > 25*3600_000 {
		t.Fatalf("24h = %d", now-v)
	}
	if v := statsRangeStart("7d"); now-v < 6*24*3600_000 || now-v > 8*24*3600_000 {
		t.Fatalf("7d = %d", now-v)
	}
	if v := statsRangeStart("all"); v != 0 {
		t.Fatalf("all 应为 0，得到 %d", v)
	}
}
