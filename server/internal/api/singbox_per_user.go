package api

import (
	"context"
	"strings"
)

// singboxPerUserInbounds 生成「按用户拆分的入站」（方案A）。
// 仅当开关开启时由调用方追加；每个入站只包含一个用户，tag 带 UUID 前 8 位，
// 端口来自 uuid_port_map，因此 Clash API 的连接可以反查到用户。
func singboxPerUserInbounds(tpl *singboxTemplate, users []sbUser, userPorts map[string]int, statsSecret string) []any {
	if tpl == nil || len(userPorts) == 0 {
		return nil
	}
	statsOn := strings.TrimSpace(statsSecret) != ""
	short := func(u sbUser) string {
		v := strings.TrimSpace(u.UUID)
		if len(v) >= 8 {
			return v[:8]
		}
		return v
	}
	name := func(u sbUser) string {
		if statsOn && strings.TrimSpace(u.UUID) != "" {
			return u.UUID
		}
		return u.Name
	}
	cert := strings.TrimSpace(tpl.CertPath)
	key := strings.TrimSpace(tpl.KeyPath)
	tls := map[string]any{"enabled": true, "certificate_path": cert, "key_path": key}

	out := []any{}
	for _, u := range users {
		port := userPorts[strings.TrimSpace(u.UUID)]
		if port <= 0 {
			continue
		}
		if tpl.EnableAnyTLS != 0 && u.UUID != "" {
			out = append(out, map[string]any{
				"type": "anytls", "tag": "anytls-" + short(u), "listen": "::", "listen_port": port,
				"tls": tls,
				"users": []any{map[string]any{"name": name(u), "password": u.UUID}},
			})
		}
		if tpl.EnableHY2 != 0 && u.UUID != "" {
			hy2 := map[string]any{
				"type": "hysteria2", "tag": "hy2-" + short(u), "listen": "::", "listen_port": port,
				"tls": map[string]any{
					"enabled": true, "alpn": []string{"h3"},
					"certificate_path": cert, "key_path": key,
				},
				"users": []any{map[string]any{"name": name(u), "password": u.UUID}},
			}
			opts := optMap(tpl.protoOptions(), "hy2")
			if up := intOrJS(opts["up_mbps"], 0, 0, 1<<30); up > 0 {
				hy2["up_mbps"] = up
			}
			if down := intOrJS(opts["down_mbps"], 0, 0, 1<<30); down > 0 {
				hy2["down_mbps"] = down
			}
			out = append(out, hy2)
		}
	}
	return out
}

// perUserExtraFn 返回注入回调：开关开启时按用户拆入站，关闭时返回 nil。
func (h *handlers) perUserExtraFn(ctx context.Context, tpl *singboxTemplate, serverID int64, statsSecret string) func([]sbUser, map[string]int) []any {
	return func(users []sbUser, _ map[string]int) []any {
		if !h.perUserInboundsEnabled(ctx) {
			return nil
		}
		uuids := make([]string, 0, len(users))
		for _, u := range users {
			uuids = append(uuids, u.UUID)
		}
		ports, err := h.ensureUUIDPorts(ctx, serverID, uuids)
		if err != nil {
			return nil
		}
		return singboxPerUserInbounds(tpl, users, ports, statsSecret)
	}
}
