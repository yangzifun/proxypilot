package api

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const singboxDefaultSSMethod = "2022-blake3-aes-256-gcm"

// singboxPortDelta 的顺序与 Worker 的 Object.keys 一致（端口冲突处理依赖顺序）。
// V15.6：Xray 下线，vless-xhttp 与 13708 端口一并移除。
var singboxPortDeltaOrder = []string{"hy2", "ss", "anytls", "vmess", "reality", "vlws", "masque"}

var singboxPortDelta = map[string]int{
	"hy2": 1, "ss": 2, "anytls": 3, "vmess": 4, "reality": 5, "vlws": 6, "masque": 8,
}

type singboxTemplate struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PortBase          int64  `json:"port_base"`
	WarpBase          int64  `json:"warp_base"`
	CertPath          string `json:"cert_path"`
	KeyPath           string `json:"key_path"`
	SSMethod          string `json:"ss_method"`
	SSServerKey       string `json:"ss_server_key"`
	RealityPrivateKey string `json:"reality_private_key"`
	RealityPublicKey  string `json:"reality_public_key"`
	RealityShortID    string `json:"reality_short_id"`
	RealityDest       string `json:"reality_dest"`
	RealitySNI        string `json:"reality_sni"`
	WSSNI             string `json:"ws_sni"`
	ProtocolOptions   string `json:"protocol_options"`
	UsersJSON         string `json:"users_json"`
	EnableSS          int64  `json:"enable_ss"`
	EnableAnyTLS      int64  `json:"enable_anytls"`
	EnableHY2         int64  `json:"enable_hy2"`
	EnableVMess       int64  `json:"enable_vmess"`
	EnableReality     int64  `json:"enable_reality"`
	EnableVLWS        int64  `json:"enable_vlws"`
	EnableMasque      int64  `json:"enable_masque"`
	EnableSingbox     *int64 `json:"enable_singbox"`
	MasquePath        string `json:"masque_path"`
	SingboxVersion    string `json:"singbox_version"`
}

type sbUser struct {
	Name       string `json:"name"`
	UUID       string `json:"uuid"`
	SSPassword string `json:"ss_password"`
}

func singboxParseUsers(raw []sbUser) []sbUser {
	out := make([]sbUser, 0, len(raw))
	for _, u := range raw {
		if strings.TrimSpace(u.UUID) == "" {
			continue
		}
		name := strings.TrimSpace(u.Name)
		if name == "" {
			name = "user" + strconv.Itoa(len(out)+1)
		}
		out = append(out, sbUser{Name: name, UUID: strings.TrimSpace(u.UUID), SSPassword: strings.TrimSpace(u.SSPassword)})
	}
	return out
}

func (t *singboxTemplate) protoOptions() map[string]any {
	if strings.TrimSpace(t.ProtocolOptions) == "" {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal([]byte(t.ProtocolOptions), &out) != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func optMap(m map[string]any, key string) map[string]any {
	if v, ok := m[key].(map[string]any); ok {
		return v
	}
	return map[string]any{}
}

// intOrJS 复刻 Worker 的 intOr：null/非数字 → fallback，否则 clamp。
func intOrJS(v any, fallback, min, max int64) int64 {
	if v == nil {
		return fallback
	}
	var n int64
	switch x := v.(type) {
	case float64:
		n = int64(x)
	case int:
		n = int64(x)
	case int64:
		n = x
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return fallback
		}
		n = parsed
	case json.Number:
		parsed, err := x.Int64()
		if err != nil {
			return fallback
		}
		n = parsed
	default:
		return fallback
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// singboxPorts 计算所有对外端口（含 WARP 镜像）。
func singboxPorts(tpl *singboxTemplate) map[string]int {
	pb := intOrJS(tpl.PortBase, 13700, 1, 65000)
	wb := intOrJS(tpl.WarpBase, 13800, 1, 65000)
	opts := tpl.protoOptions()

	p := map[string]int{}
	for _, key := range singboxPortDeltaOrder {
		custom := intOrJS(optMap(opts, key)["port"], 0, 0, 65000)
		port := int(pb) + singboxPortDelta[key]
		if custom > 0 {
			port = int(custom)
		}
		p[key] = port
		p[key+"_warp"] = int(wb) + (port - int(pb))
	}

	used := map[int]bool{}
	for _, k := range []string{"hy2", "ss", "anytls", "vmess", "reality", "vlws"} {
		v := p[k]
		if v == 0 {
			continue
		}
		if used[v] {
			fallback := int(pb) + singboxPortDelta[k]
			if !used[fallback] {
				p[k] = fallback
			}
			p[k+"_warp"] = p[k] + int(wb-pb)
		}
		used[p[k]] = true
	}
	p["base"] = int(pb)
	p["warp_base"] = int(wb)
	return p
}

func singboxWsPath(p string) string {
	v := strings.TrimSpace(p)
	if v == "" {
		return "/"
	}
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "/" + v
}

// singboxStatsPorts 计算统计控制面的本机端口（clash_api / v2ray_api）。
// 固定 port_base+90 / +91，撞上协议端口时顺延 +100（只绑 127.0.0.1，不对外）。
func singboxStatsPorts(ports map[string]int) (clash, v2ray int) {
	base := ports["base"]
	if base <= 0 {
		base = 13700
	}
	used := map[int]bool{}
	for _, k := range []string{"hy2", "ss", "anytls", "vmess", "reality", "vlws", "masque"} {
		if v := ports[k]; v > 0 {
			used[v] = true
		}
	}
	clash = base + 90
	for used[clash] {
		clash += 100
	}
	v2ray = base + 91
	for used[v2ray] || v2ray == clash {
		v2ray += 100
	}
	return clash, v2ray
}

// singboxRenderSb 渲染 sb.json（map 形式，调用方负责序列化）。
// statsSecret 非空时注入 experimental.clash_api；userStats 为真时再加 v2ray_api
// （仅自建 sing-box 支持），并把入站用户名切成 UUID（稳定唯一），避免列表重排导致流量串号。
func singboxRenderSb(tpl *singboxTemplate, users []sbUser, ports map[string]int, statsSecret string, userStats bool, cdnDomain string) map[string]any {
	inbounds := []any{}
	tlsPair := func() map[string]any {
		return map[string]any{
			"certificate_path": strings.TrimSpace(tpl.CertPath),
			"key_path":         strings.TrimSpace(tpl.KeyPath),
		}
	}
	ssKey := strings.TrimSpace(tpl.SSServerKey)
	opts := tpl.protoOptions()
	statsOn := strings.TrimSpace(statsSecret) != ""
	// 统计开启时用 UUID 作为入站用户名：稳定唯一，重排不会串号；关闭时保持原来的 userNN。
	userName := func(u sbUser) string {
		if statsOn {
			if v := strings.TrimSpace(u.UUID); v != "" {
				return v
			}
		}
		return u.Name
	}

	if tpl.EnableSS != 0 {
		ssOpt := optMap(opts, "ss")
		method := strings.TrimSpace(jsonStr(ssOpt["method"]))
		if method == "" {
			method = firstNonEmpty(strings.TrimSpace(tpl.SSMethod), singboxDefaultSSMethod)
		}
		ssUsers := make([]any, 0, len(users))
		for _, u := range users {
			if u.SSPassword != "" {
				ssUsers = append(ssUsers, map[string]any{"name": userName(u), "password": u.SSPassword})
			}
		}
		inbounds = append(inbounds, map[string]any{
			"type": "shadowsocks", "tag": "ss2022-sb", "listen": "::", "listen_port": ports["ss"],
			"method": method, "password": ssKey, "users": ssUsers,
		})
	}
	if tpl.EnableAnyTLS != 0 {
		u := make([]any, 0, len(users))
		for _, x := range users {
			u = append(u, map[string]any{"name": userName(x), "password": x.UUID})
		}
		inbounds = append(inbounds, map[string]any{
			"type": "anytls", "tag": "anytls-sb", "listen": "::", "listen_port": ports["anytls"],
			"tls":   map[string]any{"enabled": true, "certificate_path": tlsPair()["certificate_path"], "key_path": tlsPair()["key_path"]},
			"users": u,
		})
	}
	if tpl.EnableHY2 != 0 {
		hy2Opt := optMap(opts, "hy2")
		hy2 := map[string]any{
			"type": "hysteria2", "tag": "hy2-sb", "listen": "::", "listen_port": ports["hy2"],
			"tls": map[string]any{
				"enabled": true, "alpn": []string{"h3"},
				"certificate_path": tlsPair()["certificate_path"], "key_path": tlsPair()["key_path"],
			},
		}
		u := make([]any, 0, len(users))
		for _, x := range users {
			u = append(u, map[string]any{"name": userName(x), "password": x.UUID})
		}
		hy2["users"] = u
		if up := intOrJS(hy2Opt["up_mbps"], 0, 0, 1<<30); up > 0 {
			hy2["up_mbps"] = up
		}
		if down := intOrJS(hy2Opt["down_mbps"], 0, 0, 1<<30); down > 0 {
			hy2["down_mbps"] = down
		}
		inbounds = append(inbounds, hy2)
	}
	if tpl.EnableVMess != 0 {
		u := make([]any, 0, len(users))
		for _, x := range users {
			u = append(u, map[string]any{"name": userName(x), "uuid": x.UUID, "password": x.UUID})
		}
		inbounds = append(inbounds, map[string]any{
			"type": "tuic", "tag": "tuic-sb", "listen": "::", "listen_port": ports["vmess"],
			"congestion_control": "bbr", "auth_timeout": "3s", "zero_rtt_handshake": false,
			"tls": map[string]any{
				"enabled": true, "alpn": []string{"h3"},
				"certificate_path": tlsPair()["certificate_path"], "key_path": tlsPair()["key_path"],
			},
			"users": u,
		})
	}
	if tpl.EnableReality != 0 {
		rOpt := optMap(opts, "reality")
		dest := firstNonEmpty(strings.TrimSpace(jsonStr(rOpt["dest"])), strings.TrimSpace(tpl.RealityDest), "www.microsoft.com:443")
		destHost, destPort := dest, int64(443)
		if idx := strings.LastIndex(dest, ":"); idx > 0 {
			destHost = dest[:idx]
			if p, err := strconv.ParseInt(dest[idx+1:], 10, 64); err == nil {
				destPort = p
			}
		}
		u := make([]any, 0, len(users))
		for _, x := range users {
			u = append(u, map[string]any{"name": userName(x), "uuid": x.UUID, "flow": "xtls-rprx-vision"})
		}
		inbounds = append(inbounds, map[string]any{
			"type": "vless", "tag": "vless-tcp-reality-vision", "listen": "::", "listen_port": ports["reality"],
			"tls": map[string]any{
				"enabled":     true,
				"server_name": firstNonEmpty(strings.TrimSpace(jsonStr(rOpt["server_name"])), strings.TrimSpace(tpl.RealitySNI), destHost),
				"reality": map[string]any{
					"enabled": true,
					"handshake": map[string]any{
						"server": destHost, "server_port": destPort,
					},
					"private_key": strings.TrimSpace(tpl.RealityPrivateKey),
					"short_id":    []string{firstNonEmpty(strings.TrimSpace(jsonStr(rOpt["short_id"])), strings.TrimSpace(tpl.RealityShortID))},
				},
			},
			"users": u,
		})
	}
	if tpl.EnableVLWS != 0 {
		u := make([]any, 0, len(users))
		for _, x := range users {
			u = append(u, map[string]any{"name": userName(x), "uuid": x.UUID, "flow": ""})
		}
		wsPath := singboxWsPath(firstNonEmpty(strings.TrimSpace(jsonStr(optMap(opts, "vlws")["path"])), "/vlws"))
		tlsBlock := func() map[string]any {
			return map[string]any{
				"enabled": true, "alpn": []string{"h2", "http/1.1"},
				"certificate_path": tlsPair()["certificate_path"], "key_path": tlsPair()["key_path"],
			}
		}
		inbounds = append(inbounds, map[string]any{
			"type": "vless", "tag": "vless-ws-in", "listen": "::", "listen_port": ports["vlws"],
			"tls":       tlsBlock(),
			"transport": map[string]any{"type": "ws", "path": wsPath},
			"users":     u,
		})
		// CDN / Cloudflare Tunnel 回源：隧道在本机以明文 HTTP 转发，
		// 所以这里额外开一个「仅回环、无 TLS」的 WS 入站（默认 13716，可被模板 vlws.cdn_port 覆盖）。
		if strings.TrimSpace(cdnDomain) != "" {
			cdnPort := int(jsonInt(optMap(opts, "vlws")["cdn_port"]))
			if cdnPort <= 0 {
				// 默认 13707（base+7）；MASQUE 已改用 13708，不再冲突
				cdnPort = ports["vlws"] + 1
			}
			inbounds = append(inbounds, map[string]any{
				"type": "vless", "tag": "vless-ws-cdn", "listen": "127.0.0.1", "listen_port": cdnPort,
				"transport": map[string]any{"type": "ws", "path": wsPath},
				"users":     u,
			})
		}
	}

	endpoints := []any{}
	if tpl.EnableMasque != 0 {
		mqOpt := optMap(opts, "masque")
		u := make([]any, 0, len(users))
		for _, x := range users {
			pwd := x.SSPassword
			if pwd == "" {
				pwd = x.UUID
			}
			u = append(u, map[string]any{"username": x.UUID, "password": pwd})
		}
		versions := []any{1, 2, 3}
		if arr, ok := mqOpt["versions"].([]any); ok && len(arr) > 0 {
			versions = arr
		}
		endpoints = append(endpoints, map[string]any{
			"type": "masque-server", "tag": "masque-in", "listen": "::", "listen_port": ports["masque"],
			"tls": map[string]any{
				"enabled": true, "alpn": []string{"h2", "h3"},
				"certificate_path": tlsPair()["certificate_path"], "key_path": tlsPair()["key_path"],
			},
			"users":   u,
			"path":    firstNonEmpty(strings.TrimSpace(jsonStr(mqOpt["path"])), "/.well-known/masque/ip/{target}/{ipproto}/"),
			"version": versions,
		})
	}

	cfg := map[string]any{
		"log":       map[string]any{"disabled": false, "level": "warning", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct"}},
		"route": map[string]any{
			"final":                   "direct",
			"default_domain_resolver": map[string]any{"server": "dns-remote", "strategy": "prefer_ipv4"},
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{"action": "resolve", "strategy": "prefer_ipv4"},
				map[string]any{"ip_cidr": []string{"::/0", "0.0.0.0/0"}, "outbound": "direct"},
			},
		},
		"dns": map[string]any{"servers": []any{
			map[string]any{"tag": "dns-remote", "type": "udp", "server": "1.1.1.1"},
			map[string]any{"tag": "dns-remote2", "type": "udp", "server": "8.8.8.8"},
			map[string]any{"tag": "localv4", "type": "local"},
		}},
	}
	if len(endpoints) > 0 {
		cfg["endpoints"] = endpoints
	}
	if statsOn {
		clashPort, v2rayPort := singboxStatsPorts(ports)
		experimental := map[string]any{
			"clash_api": map[string]any{
				"external_controller": fmt.Sprintf("127.0.0.1:%d", clashPort),
				"secret":              strings.TrimSpace(statsSecret),
			},
		}
		// 官方 sing-box 没有 with_v2ray_api 标签，写入这段会让 sing-box check 失败
		if userStats {
			userList := make([]any, 0, len(users))
			for _, u := range users {
				if n := userName(u); n != "" {
					userList = append(userList, n)
				}
			}
			experimental["v2ray_api"] = map[string]any{
				"listen": fmt.Sprintf("127.0.0.1:%d", v2rayPort),
				"stats":  map[string]any{"enabled": true, "users": userList},
			}
		}
		cfg["experimental"] = experimental
	}
	return cfg
}

// singboxInstallVersions 生成随 deploy 下发的引擎版本（Agent 按需安装/切换）。
// V15.6：Xray 下线，只下发 sing-box。
func singboxInstallVersions(tpl *singboxTemplate) string {
	m := map[string]string{}
	if tpl.engineOn("singbox") && strings.TrimSpace(tpl.SingboxVersion) != "" {
		m["sing-box"] = strings.TrimSpace(tpl.SingboxVersion)
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// cdnPortOf CDN 明文 WS 回源端口：默认 base+7（13707），可用模板 vlws.cdn_port 覆盖。
func cdnPortOf(tpl *singboxTemplate, ports map[string]int) int {
	if v := int(jsonInt(optMap(tpl.protoOptions(), "vlws")["cdn_port"])); v > 0 {
		return v
	}
	return ports["vlws"] + 1
}
