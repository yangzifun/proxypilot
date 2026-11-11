// Package config 解析 Agent 配置（/etc/proxypilot-agent.conf）。
//
// 兼容 V8.1 shell agent 的写法：每行 KEY=VALUE 或 KEY="VALUE"，# 开头为注释；
// 键名同时接受 PROXYPILOT_* 与短名（origin / key / interval / state_dir / services）。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultPath 默认配置文件路径（安装脚本写入）。
	DefaultPath = "/etc/proxypilot-agent.conf"
	// DefaultInterval 兜底轮询间隔（秒）；服务端会通过 next_poll_sec 覆盖。
	DefaultInterval = 2
	// DefaultStateDir 状态目录（next_poll / applied 等状态文件）。
	DefaultStateDir = "/var/lib/proxypilot-agent"
	// KeyPrefix 接入密钥前缀（与 Worker 侧 AGENT_TOKEN_PREFIX 一致）。
	KeyPrefix = "ag_"
	// MinInterval 轮询间隔下限（与 Worker 的 AGENT_POLL_MIN_SEC 一致）。
	MinInterval = 2
	// MaxInterval 轮询间隔上限（与 Worker 的 AGENT_POLL_MAX_SEC 一致）。
	MaxInterval = 900
)

// DefaultServices 默认探测的 systemd 服务（V15.6 起不再探测 xray）。
// cloudflared: Cloudflare Tunnel；warp-svc: Cloudflare WARP 客户端 systemd 单元
var DefaultServices = []string{"sing-box", "mihomo", "mtg", "cloudflared", "warp-svc", "proxypilot-agent"}

// Config 是 Agent 的全部运行参数。
type Config struct {
	Origin   string   // 兜底地址（公网），如 https://pp.example.com
	InternalURL string // 内网地址（优先），如 http://100.96.0.18:8080
	MetricMS    int    // 内网采样间隔（毫秒）
	Key      string   // 接入密钥（ag_ 开头）
	Interval int      // 兜底轮询间隔（秒）
	StateDir string   // 状态目录
	Services []string // 需要上报 systemd 状态的服务名
	Insecure bool     // 跳过 TLS 证书校验（仅排查用）
	AllowURLMigration bool // 允许按服务端下发的 agent_access_url 自动迁移

	// 节点维护：每日定时重启（错峰）。默认开启，04:00 起随机延后。
	RebootEnabled   bool
	RebootTime      string // HH:MM，默认 04:00
	RebootDelaySec  int    // 随机延后上限（秒），默认 2700 = 45 分钟
}

// Defaults 返回默认配置（未读取任何文件）。
func Defaults() Config {
	return Config{
		Interval: DefaultInterval,
		MetricMS: 200,
		StateDir: DefaultStateDir,
		Services: append([]string(nil), DefaultServices...),
		AllowURLMigration: true,
		RebootEnabled:     true,
		RebootTime:        "04:00",
		RebootDelaySec:    2700,
	}
}

// Parse 解析配置文本（纯函数，便于单测）。
func Parse(text string) Config {
	c := Defaults()
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(line[:eq]))
		val := unquote(strings.TrimSpace(line[eq+1:]))
		switch key {
		case "PROXYPILOT_URL", "ORIGIN", "URL":
			c.Origin = strings.TrimRight(val, "/")
		case "PROXYPILOT_INTERNAL_URL", "INTERNAL_URL":
			c.InternalURL = strings.TrimRight(val, "/")
		case "PROXYPILOT_METRIC_MS", "METRIC_MS":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				c.MetricMS = n
			}
		case "PROXYPILOT_TOKEN", "TOKEN", "KEY":
			c.Key = val
		case "PROXYPILOT_INTERVAL", "INTERVAL":
			if n, err := strconv.Atoi(val); err == nil && n > 0 {
				c.Interval = clampInterval(n)
			}
		case "PROXYPILOT_STATE_DIR", "STATE_DIR":
			if val != "" {
				c.StateDir = val
			}
		case "PROXYPILOT_SERVICES", "SERVICE_LIST", "SERVICES":
			// 与默认集合合并：配置里写了自定义列表也不丢掉 cloudflared 等基础探针
			seen := map[string]bool{}
			merged := make([]string, 0, len(DefaultServices)+4)
			for _, name := range append(splitList(val), DefaultServices...) {
				if name == "" || seen[name] {
					continue
				}
				seen[name] = true
				merged = append(merged, name)
			}
			c.Services = merged
		case "PROXYPILOT_ALLOW_URL_MIGRATION", "ALLOW_URL_MIGRATION":
			c.AllowURLMigration = truthy(val)
		case "PROXYPILOT_INSECURE", "INSECURE":
			c.Insecure = truthy(val)
		case "REBOOT_ENABLED", "PROXYPILOT_REBOOT_ENABLED", "DAILY_REBOOT":
			c.RebootEnabled = truthy(val)
		case "REBOOT_TIME", "PROXYPILOT_REBOOT_TIME":
			if val != "" {
				c.RebootTime = val
			}
		case "REBOOT_DELAY_SEC", "PROXYPILOT_REBOOT_DELAY_SEC":
			if n, err := strconv.Atoi(val); err == nil && n >= 0 {
				c.RebootDelaySec = n
			}
		}
	}
	return c
}

// Load 读取配置文件；文件不存在时返回默认值（error 非空，调用方自行决定是否致命）。
// 环境变量优先于文件，便于临时排查与容器化部署。
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		c := Defaults()
		ApplyEnv(&c)
		return c, fmt.Errorf("读取配置失败（%s）：%v", path, err)
	}
	c := Parse(string(b))
	ApplyEnv(&c)
	return c, nil
}

// ApplyEnv 用环境变量覆盖配置（空值忽略）。
func ApplyEnv(c *Config) {
	if v := strings.TrimSpace(os.Getenv("PROXYPILOT_URL")); v != "" {
		c.Origin = strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("PROXYPILOT_TOKEN")); v != "" {
		c.Key = v
	}
	if v := strings.TrimSpace(os.Getenv("PROXYPILOT_INTERVAL")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Interval = clampInterval(n)
		}
	}
	if v := strings.TrimSpace(os.Getenv("PROXYPILOT_STATE_DIR")); v != "" {
		c.StateDir = v
	}
	if v := strings.TrimSpace(os.Getenv("PROXYPILOT_INSECURE")); v != "" && truthy(v) {
		c.Insecure = true
	}
}

// Validate 校验必填项（与后台安装命令一致：origin + ag_ 密钥）。
func (c Config) Validate() error {
	if c.Origin == "" {
		return fmt.Errorf("缺少 PROXYPILOT_URL（Worker 地址，如 https://pp.example.com）")
	}
	if !strings.HasPrefix(c.Origin, "http://") && !strings.HasPrefix(c.Origin, "https://") {
		return fmt.Errorf("PROXYPILOT_URL 必须以 http:// 或 https:// 开头（当前 %q）", c.Origin)
	}
	if c.Key == "" {
		return fmt.Errorf("缺少 PROXYPILOT_TOKEN（后台「服务器管理 → 生成密钥」获取）")
	}
	if !strings.HasPrefix(c.Key, KeyPrefix) {
		return fmt.Errorf("接入密钥格式无效（应以 %s 开头）", KeyPrefix)
	}
	if len(strings.TrimPrefix(c.Key, KeyPrefix)) < 16 {
		return fmt.Errorf("接入密钥长度不足（应为 %s + 64 位十六进制）", KeyPrefix)
	}
	if c.Interval < MinInterval || c.Interval > MaxInterval {
		return fmt.Errorf("轮询间隔应在 %d–%d 秒之间（当前 %d）", MinInterval, MaxInterval, c.Interval)
	}
	return nil
}

// String 渲染为配置文件内容（安装器写入；KEY="VALUE" 风格与 shell agent 一致）。
func (c Config) String() string {
	var b strings.Builder
	b.WriteString("# ProxyPilot Agent 配置（由安装脚本或 proxypilot-agent install 生成）\n")
	b.WriteString("# 修改后执行：systemctl restart proxypilot-agent\n")
	b.WriteString(`PROXYPILOT_URL="` + c.Origin + "\"\n")
	b.WriteString(`PROXYPILOT_TOKEN="` + c.Key + "\"\n")
	b.WriteString("INTERVAL=" + strconv.Itoa(c.Interval) + "\n")
	b.WriteString(`STATE_DIR="` + c.StateDir + "\"\n")
	b.WriteString(`SERVICE_LIST="` + strings.Join(c.Services, " ") + "\"\n")
	if c.Insecure {
		b.WriteString("PROXYPILOT_INSECURE=1\n")
	}
	// 节点维护：每日错峰重启（默认开启，04:00 起随机延后 45 分钟）
	b.WriteString("\n# 节点维护：每日定时重启（多机通过随机延后错开，避免同时掉线）\n")
	if c.RebootEnabled {
		b.WriteString("REBOOT_ENABLED=1\n")
	} else {
		b.WriteString("REBOOT_ENABLED=0\n")
	}
	b.WriteString("REBOOT_TIME=\"" + firstNonEmpty(c.RebootTime, "04:00") + "\"\n")
	b.WriteString("REBOOT_DELAY_SEC=" + strconv.Itoa(c.RebootDelaySec) + "\n")
	return b.String()
}

func clampInterval(n int) int {
	if n < MinInterval {
		return MinInterval
	}
	if n > MaxInterval {
		return MaxInterval
	}
	return n
}

func unquote(v string) string {
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			return v[1 : len(v)-1]
		}
	}
	return v
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// splitList 按空格/逗号切分服务名（过滤空项与非法字符；全非法时回落默认列表）。
func splitList(v string) []string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if !validServiceName(f) {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return append([]string(nil), DefaultServices...)
	}
	return out
}

func validServiceName(v string) bool {
	if v == "" || len(v) > 64 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '@' || r == '.' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

// firstNonEmpty 返回第一个非空值。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}