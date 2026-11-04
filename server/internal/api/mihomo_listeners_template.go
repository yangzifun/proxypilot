package api

// 模板模式：直接使用模板中的 listeners（自带端口），按 UUID 注入用户
import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func renderMihomoListenersTemplate(cfg mihomoServerConfig, uuids []string) (string, map[string]int, string) {
	var b strings.Builder
	b.WriteString("# 由 ProxyPilot 生成，请勿手工修改\n")
	b.WriteString("mixed-port: 7890\nsocks-port: 7891\nallow-lan: true\nmode: rule\nlog-level: warning\n")
	b.WriteString("external-controller: 127.0.0.1:9090\n")
	if strings.TrimSpace(cfg.Secret) != "" {
		b.WriteString("secret: " + mihomoQuote(cfg.Secret) + "\n")
	}
	b.WriteString("listeners:\n")
	ports := map[string]int{}
	seen := 0
	for _, raw := range cfg.Listeners {
		if len(raw) == 0 {
			continue
		}
		// 深拷贝，避免修改模板对象
		item := map[string]any{}
		for k, v := range raw {
			item[k] = v
		}
		typ := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		name := strings.TrimSpace(fmt.Sprint(item["name"]))
		switch typ {
		case "vless", "vmess":
			users := make([]map[string]any, 0, len(uuids))
			for _, u := range uuids {
				users = append(users, map[string]any{"username": u, "uuid": u})
			}
			item["users"] = users
		case "trojan", "anytls", "hysteria2", "hy2", "tuic":
			users := map[string]any{}
			for _, u := range uuids {
				users[u] = u
			}
			item["users"] = users
		case "shadowsocks":
			// 保留模板中的 cipher/password；有 users 时按 uuid 生成
			if _, ok := item["users"]; ok {
				users := map[string]any{}
				for _, u := range uuids {
					users[u] = u
				}
				item["users"] = users
			}
		}
		out, err := yaml.Marshal(item)
		if err != nil {
			return "", ports, "模板 listeners 解析失败：" + err.Error()
		}
		lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
		for i, line := range lines {
			if i == 0 {
				b.WriteString("  - " + line + "\n")
			} else {
				b.WriteString("    " + line + "\n")
			}
		}
		if p, ok := item["port"]; ok {
			ports[name] = toIntAny(p)
		}
		seen++
	}
	if seen == 0 {
		return "", ports, "模板中没有可用 listeners"
	}
	return b.String(), ports, ""
}

func toIntAny(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n := 0
		fmt.Sscanf(x, "%d", &n)
		return n
	}
	return 0
}
