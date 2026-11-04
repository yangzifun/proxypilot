package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// buildMihomoServerProxies 把「mihomo 服务端」绑定的 listeners 转成客户端 proxies。
func (h *handlers) buildMihomoServerProxies(ctx context.Context, uuid string) []map[string]any {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT n.id, n.name, COALESCE(n.uuid_list,'[]') AS uuid_list, COALESCE(n.port_base,0) AS port_base,
		        COALESCE(n.domain,'') AS domain, COALESCE(s.host,'') AS host, COALESCE(s.last_ip4,'') AS ip4,
		        COALESCE(t.server_json,'{}') AS server_json
		   FROM mihomo_nodes n
		   JOIN mihomo_templates t ON t.id = n.template_id AND COALESCE(t.mode,'subscription') = 'server'
		   LEFT JOIN servers s ON s.id = n.server_id
		  WHERE n.enabled = 1 AND COALESCE(t.enabled,1) = 1
		  ORDER BY n.id ASC`)
	if err != nil {
		return nil
	}
	out := []map[string]any{}
	for _, row := range rows {
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		if len(list) > 0 && !mihomoContains(list, uuid) {
			continue
		}
		cfg := parseMihomoServerConfig(mapStr(row, "server_json", "{}"))
		if pb := mapInt(row, "port_base", 0); pb > 0 {
			cfg.PortBase = int(pb)
		}
		host := strings.TrimSpace(mapStr(row, "domain", ""))
		if host == "" {
			host = strings.TrimSpace(mapStr(row, "ip4", ""))
		}
		if host == "" {
			host = strings.TrimSpace(mapStr(row, "host", ""))
		}
		if host == "" {
			continue
		}
		name := mapStr(row, "name", "mihomo")
		protoSet := map[string]bool{}
		for _, p := range cfg.Protocols {
			protoSet[p] = true
		}
		sni := ""
		if len(cfg.RealitySNI) > 0 {
			sni = cfg.RealitySNI[0]
		}
		if sni == "" {
			sni = cfg.RealityDest
			if i := strings.LastIndex(sni, ":"); i > 0 {
				sni = sni[:i]
			}
		}
		add := func(p map[string]any) { out = append(out, p) }
		if protoSet["vless_ws"] {
			add(map[string]any{
				"name": name + "-vless-ws", "type": "vless", "server": host,
				"port": cfg.PortBase + 1, "uuid": uuid, "udp": true, "tls": true,
				"servername": host, "network": "ws", "skip-cert-verify": true,
				"ws-opts": map[string]any{"path": cfg.WSPath, "headers": map[string]any{"Host": host}},
			})
		}
		if protoSet["vless_reality"] {
			pub := x25519PublicKeyFromPrivate(cfg.RealityPriv)
			add(map[string]any{
				"name": name + "-vless-reality", "type": "vless", "server": host,
				"port": cfg.PortBase + 2, "uuid": uuid, "udp": true, "tls": true,
				"servername": sni, "network": "tcp", "flow": "xtls-rprx-vision",
				"reality-opts":       map[string]any{"public-key": pub, "short-id": cfg.RealitySID},
				"client-fingerprint": "chrome", "skip-cert-verify": true,
			})
		}
		if protoSet["vmess"] {
			add(map[string]any{
				"name": name + "-vmess", "type": "vmess", "server": host,
				"port": cfg.PortBase + 3, "uuid": uuid, "alterId": 0, "cipher": "auto",
				"udp": true, "tls": true, "servername": host, "network": "ws", "skip-cert-verify": true,
				"ws-opts": map[string]any{"path": cfg.WSPath, "headers": map[string]any{"Host": host}},
			})
		}
		if protoSet["trojan"] {
			add(map[string]any{
				"name": name + "-trojan", "type": "trojan", "server": host,
				"port": cfg.PortBase + 4, "password": uuid, "udp": true,
				"sni": host, "skip-cert-verify": true,
			})
		}
		if protoSet["anytls"] {
			add(map[string]any{
				"name": name + "-anytls", "type": "anytls", "server": host,
				"port": cfg.PortBase + 5, "password": uuid, "udp": true,
				"sni": host, "skip-cert-verify": true,
			})
		}
		if protoSet["hy2"] {
			add(map[string]any{
				"name": name + "-hy2", "type": "hysteria2", "server": host,
				"port": cfg.PortBase + 6, "password": uuid,
				"sni": host, "skip-cert-verify": true,
			})
		}
		if protoSet["tuic"] {
			add(map[string]any{
				"name": name + "-tuic", "type": "tuic", "server": host,
				"port": cfg.PortBase + 7, "uuid": uuid, "password": uuid,
				"sni": host, "skip-cert-verify": true, "udp-relay-mode": "native",
			})
		}
		if protoSet["socks"] {
			add(map[string]any{
				"name": name + "-socks", "type": "socks5", "server": host,
				"port": cfg.PortBase + 8, "username": uuid, "password": uuid, "udp": true,
			})
		}
		if protoSet["http"] {
			add(map[string]any{
				"name": name + "-http", "type": "http", "server": host,
				"port": cfg.PortBase + 9, "username": uuid, "password": uuid,
			})
		}
		if protoSet["ss"] {
			add(map[string]any{
				"name": name + "-ss", "type": "ss", "server": host,
				"port": cfg.PortBase + 10, "cipher": cfg.SSMethod, "password": uuid, "udp": true,
			})
		}
		_ = fmt.Sprint
		_ = strconv.Itoa
	}
	return out
}
