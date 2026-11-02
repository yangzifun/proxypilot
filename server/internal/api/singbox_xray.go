package api

import (
	"fmt"
	"strings"
)

// singboxSpiderXPath 复刻 Worker 的 FNV-1a 双哈希诱饵路径。
func singboxSpiderXPath(seed string) string {
	var a uint32 = 2166136261
	var b uint32 = 2166136261
	for _, r := range seed {
		v := uint32(r)
		a = (a ^ v) * 16777619
		b = (b ^ (v + 31)) * 16777619
	}
	return fmt.Sprintf("/spx-%08x/%08x", a, b)
}

func optXHTTPPath(tpl *singboxTemplate) string {
	return firstNonEmpty(strings.TrimSpace(jsonStr(optMap(tpl.protoOptions(), "xhttp")["path"])),
		strings.TrimSpace(tpl.XHTTPPath), "/xhttp-auto")
}

// singboxRenderXray 渲染 xray.json（vless-xhttp over REALITY）。
func singboxRenderXray(tpl *singboxTemplate, users []sbUser, ports map[string]int) map[string]any {
	dest := firstNonEmpty(strings.TrimSpace(tpl.RealityDest), "www.microsoft.com:443")
	sni := firstNonEmpty(strings.TrimSpace(tpl.RealitySNI), strings.Split(dest, ":")[0], "www.microsoft.com")
	xhttpPath := optXHTTPPath(tpl)
	spiderX := singboxSpiderXPath(tpl.Name + "|" + tpl.RealityShortID + "|" + xhttpPath)

	clients := make([]any, 0, len(users))
	for _, u := range users {
		clients = append(clients, map[string]any{"id": u.UUID, "flow": ""})
	}
	return map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"dns": map[string]any{"servers": []string{"1.1.1.1", "8.8.8.8"}, "queryStrategy": "UseIPv4"},
		"inbounds": []any{map[string]any{
			"listen": "::", "port": ports["xhttp"], "protocol": "vless",
			"settings": map[string]any{"clients": clients, "decryption": "none"},
			"streamSettings": map[string]any{
				"network": "xhttp", "security": "reality",
				"realitySettings": map[string]any{
					"dest": dest, "privateKey": strings.TrimSpace(tpl.RealityPrivateKey),
					"serverNames": []string{sni}, "shortIds": []string{strings.TrimSpace(tpl.RealityShortID)},
					"spiderX": spiderX,
				},
				"xhttpSettings": map[string]any{"path": xhttpPath, "mode": "auto"},
			},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "freedom", "tag": "direct", "settings": map[string]any{"domainStrategy": "UseIPv4"},
		}},
		"routing": map[string]any{"domainStrategy": "UseIPv4"},
	}
}
