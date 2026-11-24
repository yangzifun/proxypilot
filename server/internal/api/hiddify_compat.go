package api

import (
	"net/http"
	"strings"
)

// isHiddifyUserAgent 判断是否为 Hiddify 客户端（大小写不敏感）。
func isHiddifyUserAgent(ua string) bool {
	return strings.Contains(strings.ToLower(ua), "hiddify")
}

// hiddifyCompatLine 对单条代理 URI 做安全参数后处理；非目标 scheme 原样返回。
//
//	anytls / hysteria2 / hy2 : insecure=0（删除 insecure=1/重复项）
//	tuic                     : allow_insecure=0（删除 allow_insecure=1/重复项）
func hiddifyCompatLine(line string) string {
	lower := strings.ToLower(line)
	targetKey := ""
	switch {
	case strings.HasPrefix(lower, "anytls://"), strings.HasPrefix(lower, "hysteria2://"), strings.HasPrefix(lower, "hy2://"):
		targetKey = "insecure"
	case strings.HasPrefix(lower, "tuic://"):
		targetKey = "allow_insecure"
	default:
		return line
	}

	// 保留 #fragment；只处理 URI 主干的 query。
	fragment := ""
	main := line
	if i := strings.IndexByte(main, '#'); i >= 0 {
		fragment = main[i:]
		main = main[:i]
	}
	base, rawQuery, hasQuery := main, "", false
	if i := strings.IndexByte(main, '?'); i >= 0 {
		base, rawQuery, hasQuery = main[:i], main[i+1:], true
	}

	parts := []string{}
	if hasQuery {
		for _, p := range strings.Split(rawQuery, "&") {
			if p == "" {
				continue
			}
			key := p
			if j := strings.IndexByte(p, '='); j >= 0 {
				key = p[:j]
			}
			if strings.EqualFold(key, targetKey) {
				continue
			}
			parts = append(parts, p)
		}
	}
	parts = append(parts, targetKey+"=0")
	return base + "?" + strings.Join(parts, "&") + fragment
}

// applyHiddifyCompat 对订阅链接列表做 Hiddify 安全参数补齐；普通 UA 不调用。
func applyHiddifyCompat(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = hiddifyCompatLine(line)
	}
	return out
}

// maybeApplyHiddifyCompat 仅在 UA 含 hiddify 时处理 `/sub` 与 `/batch-configs`。
func maybeApplyHiddifyCompat(r *http.Request, lines []string) []string {
	if r == nil || !isHiddifyUserAgent(r.Header.Get("User-Agent")) {
		return lines
	}
	return applyHiddifyCompat(lines)
}
