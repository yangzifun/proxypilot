package config

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.Interval != DefaultInterval || c.StateDir != DefaultStateDir {
		t.Fatalf("默认值不对：%+v", c)
	}
	if len(c.Services) != len(DefaultServices) {
		t.Fatalf("默认服务列表不对：%v", c.Services)
	}
}

func TestParseLegacyShellStyle(t *testing.T) {
	c := Parse(strings.Join([]string{
		"# ProxyPilot Agent 配置",
		"",
		`PROXYPILOT_URL="https://pp.example.com/"`,
		`PROXYPILOT_TOKEN="ag_0123456789abcdef0123456789abcdef"`,
		"INTERVAL=30",
		`STATE_DIR="/tmp/pp"`,
		`SERVICE_LIST="xray sing-box bad;svc,nginx"`,
	}, "\n"))
	if c.Origin != "https://pp.example.com" {
		t.Fatalf("origin=%q（应去掉尾部斜杠）", c.Origin)
	}
	if c.Key != "ag_0123456789abcdef0123456789abcdef" {
		t.Fatalf("key=%q", c.Key)
	}
	if c.Interval != 30 || c.StateDir != "/tmp/pp" {
		t.Fatalf("interval=%d stateDir=%q", c.Interval, c.StateDir)
	}
	// 配置里的自定义列表会与默认集合合并（保证 cloudflared 等基础探针不被丢掉）
	want := append([]string{"xray", "sing-box", "nginx"}, DefaultServices...)
	want = dedupServices(want)
	if got := strings.Join(c.Services, ","); got != strings.Join(want, ",") {
		t.Fatalf("services=%q 期望 %q（自定义 + 默认并集，非法名被过滤）", got, strings.Join(want, ","))
	}
}

func TestParseShortKeys(t *testing.T) {
	c := Parse("origin=https://a.example.com\nkey=ag_x\ninsecure=yes\n")
	if c.Origin != "https://a.example.com" || c.Key != "ag_x" || !c.Insecure {
		t.Fatalf("%+v", c)
	}
}

func TestParseClampsInterval(t *testing.T) {
	if got := Parse("INTERVAL=9999").Interval; got != MaxInterval {
		t.Fatalf("上限未生效：%d", got)
	}
	if got := Parse("INTERVAL=1").Interval; got != MinInterval {
		t.Fatalf("下限未生效：%d", got)
	}
	if got := Parse("INTERVAL=abc").Interval; got != DefaultInterval {
		t.Fatalf("非法值应回落默认：%d", got)
	}
}

func TestParseInvalidServiceListFallsBackToDefault(t *testing.T) {
	c := Parse(`SERVICE_LIST="bad;svc another|bad"`)
	if strings.Join(c.Services, ",") != strings.Join(DefaultServices, ",") {
		t.Fatalf("全非法时未回落默认：%v", c.Services)
	}
}

func TestValidate(t *testing.T) {
	good := Defaults()
	good.Origin = "https://pp.example.com"
	good.Key = "ag_" + strings.Repeat("a", 64)
	if err := good.Validate(); err != nil {
		t.Fatalf("合法配置被拒：%v", err)
	}
	cases := []struct {
		name string
		mod  func(c *Config)
		want string
	}{
		{"缺 origin", func(c *Config) { c.Origin = "" }, "PROXYPILOT_URL"},
		{"scheme 非法", func(c *Config) { c.Origin = "pp.example.com" }, "http://"},
		{"缺 key", func(c *Config) { c.Key = "" }, "PROXYPILOT_TOKEN"},
		{"前缀非法", func(c *Config) { c.Key = "tk_1234567890abcdef" }, "ag_"},
		{"长度不足", func(c *Config) { c.Key = "ag_1234" }, "长度不足"},
		{"间隔越界", func(c *Config) { c.Interval = 1 }, "轮询间隔"},
	}
	for _, tc := range cases {
		cfg := good
		tc.mod(&cfg)
		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：期望包含 %q，实际 %v", tc.name, tc.want, err)
		}
	}
}

func TestStringRoundTrip(t *testing.T) {
	cfg := Defaults()
	cfg.Origin = "https://pp.example.com"
	cfg.Key = "ag_" + strings.Repeat("f", 64)
	cfg.Interval = 120
	cfg.Services = []string{"xray", "nginx"}
	cfg.Insecure = true
	text := cfg.String()
	if !strings.Contains(text, `PROXYPILOT_TOKEN="ag_ff`) {
		t.Fatalf("配置文本缺少密钥行：%s", text)
	}
	back := Parse(text)
	if back.Origin != cfg.Origin || back.Key != cfg.Key || back.Interval != cfg.Interval || !back.Insecure {
		t.Fatalf("往返不一致：%+v", back)
	}
	wantSvc := dedupServices(append([]string{"xray", "nginx"}, DefaultServices...))
	if strings.Join(back.Services, ",") != strings.Join(wantSvc, ",") {
		t.Fatalf("services 往返不一致：%v 期望 %v", back.Services, wantSvc)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("往返后校验失败：%v", err)
	}
}

func dedupServices(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// 节点维护：每日错峰重启的配置解析与生成
func TestRebootConfig(t *testing.T) {
	c := Parse("REBOOT_ENABLED=0\nREBOOT_TIME=03:30\nREBOOT_DELAY_SEC=600\n")
	if c.RebootEnabled {
		t.Fatal("REBOOT_ENABLED=0 应关闭")
	}
	if c.RebootTime != "03:30" {
		t.Fatalf("REBOOT_TIME = %q", c.RebootTime)
	}
	if c.RebootDelaySec != 600 {
		t.Fatalf("REBOOT_DELAY_SEC = %d", c.RebootDelaySec)
	}
	// 默认值：开启、04:00、45 分钟错峰
	d := Defaults()
	if !d.RebootEnabled || d.RebootTime != "04:00" || d.RebootDelaySec != 2700 {
		t.Fatalf("默认维护配置不对：%+v", d)
	}
	out := d.String()
	for _, want := range []string{"REBOOT_ENABLED=1", `REBOOT_TIME="04:00"`, "REBOOT_DELAY_SEC=2700"} {
		if !strings.Contains(out, want) {
			t.Fatalf("配置文本缺少 %q：\n%s", want, out)
		}
	}
}