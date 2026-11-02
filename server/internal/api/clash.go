package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------------- 基础工具 ----------------

func safeDecode(s string) string {
	if v, err := url.PathUnescape(s); err == nil {
		return v
	}
	return s
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n == 0 {
		return fallback
	}
	return n
}

var portRangeRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://([^/?#]*@)?(\[[^\]]+\]|[^:/?#@]+):(\d{1,5})-(\d{1,5})`)

// parseProxyURL 兼容 hysteria2 的端口范围（host:10000-10009）。
func parseProxyURL(raw string) (*url.URL, string, int, error) {
	if m := portRangeRe.FindStringSubmatchIndex(raw); m != nil {
		endPos := m[1]
		// Go 正则不支持 lookahead：手工确认端口范围后面是 / ? # 或行尾
		if endPos == len(raw) || strings.ContainsRune("/?#", rune(raw[endPos])) {
			group := func(i int) string {
				if m[2*i] < 0 {
					return ""
				}
				return raw[m[2*i]:m[2*i+1]]
			}
			startPort := group(3)
			rangeStr := startPort + "-" + group(4)
			matched := raw[m[0]:m[1]]
			sanitized := raw[:len(matched)-len(rangeStr)] + startPort + raw[len(matched):]
			u, err := url.Parse(sanitized)
			first, _ := strconv.Atoi(startPort)
			return u, rangeStr, first, err
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", 0, err
	}
	first := 0
	if u.Port() != "" {
		first, _ = strconv.Atoi(u.Port())
	}
	return u, "", first, nil
}

func fragmentName(u *url.URL, fallback string) string {
	if u.Fragment != "" {
		return safeDecode(u.Fragment)
	}
	return fallback
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p)
	}
	return out
}

// ---------------- Shadowsocks ----------------

type ssParsed struct {
	Name     string
	Server   string
	Port     int
	Method   string
	Password string
	Query    url.Values
}

func parseShadowsocksURI(config string) (*ssParsed, error) {
	raw := strings.TrimSpace(config)
	idx := strings.Index(raw, "://")
	if idx < 0 {
		return nil, fmt.Errorf("不是 ss 链接")
	}
	body := raw[idx+3:]
	name := ""

	if h := strings.Index(body, "#"); h >= 0 {
		name = safeDecode(body[h+1:])
		body = body[:h]
	}
	query := ""
	if q := strings.Index(body, "?"); q >= 0 {
		query = body[q+1:]
		body = body[:q]
	}

	userInfo, hostPort := "", ""
	if at := strings.LastIndex(body, "@"); at >= 0 {
		userInfo, hostPort = body[:at], body[at+1:]
	} else {
		decoded, ok := decodeAnyBase64(body)
		if !ok {
			return nil, fmt.Errorf("无法解析 Shadowsocks 配置")
		}
		dec := string(decoded)
		at := strings.LastIndex(dec, "@")
		if at < 0 {
			return nil, fmt.Errorf("无法解析 Shadowsocks 配置")
		}
		userInfo, hostPort = dec[:at], dec[at+1:]
	}

	userInfoRaw := safeDecode(userInfo)
	if !strings.Contains(userInfoRaw, ":") {
		if decoded, ok := decodeAnyBase64(userInfoRaw); ok && strings.Contains(string(decoded), ":") {
			userInfoRaw = string(decoded)
		}
	}
	method, password := "aes-256-gcm", ""
	if c := strings.Index(userInfoRaw, ":"); c > 0 {
		method, password = userInfoRaw[:c], userInfoRaw[c+1:]
	} else if userInfoRaw != "" {
		method = userInfoRaw
	}

	server, port := hostPort, 8388
	if strings.HasPrefix(hostPort, "[") {
		if end := strings.Index(hostPort, "]"); end > 0 {
			server = hostPort[1:end]
			rest := hostPort[end+1:]
			if strings.HasPrefix(rest, ":") {
				port = atoiOr(rest[1:], 8388)
			}
		}
	} else if c := strings.LastIndex(hostPort, ":"); c > 0 {
		server = hostPort[:c]
		port = atoiOr(hostPort[c+1:], 8388)
	}

	params, _ := url.ParseQuery(query)
	if name == "" {
		name = fmt.Sprintf("SS-%s-%d", server, port)
	}
	return &ssParsed{Name: name, Server: server, Port: port, Method: method, Password: password, Query: params}, nil
}

type pluginInfo struct {
	Plugin            string
	Opts              map[string]any
	ClientFingerprint string
}

func parsePluginOptions(pluginParam, extraParam string) pluginInfo {
	opts := map[string]any{}
	pushPairs := func(str string) {
		for _, pair := range strings.Split(str, ";") {
			if pair == "" {
				continue
			}
			if eq := strings.Index(pair, "="); eq > 0 {
				opts[strings.TrimSpace(pair[:eq])] = strings.TrimSpace(pair[eq+1:])
			} else {
				opts[strings.TrimSpace(pair)] = true
			}
		}
	}
	plugin := ""
	if pluginParam != "" {
		parts := strings.Split(pluginParam, ";")
		plugin = strings.TrimSpace(parts[0])
		pushPairs(strings.Join(parts[1:], ";"))
	}
	if extraParam != "" {
		pushPairs(extraParam)
	}
	if v, ok := opts["version"]; ok {
		if n, err := strconv.Atoi(fmt.Sprint(v)); err == nil {
			opts["version"] = n
		}
	}
	for _, k := range []string{"tls", "mux"} {
		if v, ok := opts[k]; ok {
			opts[k] = v == true || fmt.Sprint(v) == "true" || fmt.Sprint(v) == "1"
		}
	}
	fp := ""
	switch plugin {
	case "shadow-tls", "restls", "jls":
		if v, ok := opts["client-fingerprint"]; ok {
			fp = fmt.Sprint(v)
			delete(opts, "client-fingerprint")
		} else {
			fp = "chrome"
		}
	}
	return pluginInfo{Plugin: plugin, Opts: opts, ClientFingerprint: fp}
}

// ---------------- 协议转换 ----------------

// convertToClashFormat 对应 worker.js 的同名函数；不支持的协议返回 nil。
func convertToClashFormat(config string) map[string]any {
	protocol := getProtocol(config)
	switch protocol {
	case "vmess":
		return convertVmess(config)
	case "trojan":
		return convertTrojan(config)
	case "vless", "any-reality", "reality", "anyreality":
		return convertVless(config)
	case "shadowsocks":
		return convertShadowsocks(config)
	case "hysteria2":
		return convertHysteria2(config)
	case "tuic":
		return convertTuic(config)
	case "anytls":
		return convertAnyTLS(config)
	}
	return nil
}

func convertVmess(config string) map[string]any {
	raw := strings.TrimSpace(config)
	decoded, ok := decodeAnyBase64(raw[8:])
	if !ok {
		return nil
	}
	var obj map[string]any
	if json.Unmarshal(decoded, &obj) != nil {
		return nil
	}
	str := func(k string) string {
		if v, ok := obj[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	port := atoiOr(str("port"), 443)
	name := str("ps")
	if name == "" {
		name = str("remark")
	}
	if name == "" {
		name = fmt.Sprintf("VMess-%s-%d", str("add"), port)
	}
	tls := str("tls") == "tls"
	p := map[string]any{
		"name": name, "type": "vmess", "server": str("add"), "port": port,
		"uuid": str("id"), "alterId": firstNonNil(obj["aid"], 0), "cipher": firstNonEmpty(str("scy"), "auto"),
		"udp": true, "packet-encoding": "packetaddr", "global-padding": false,
		"authenticated-length": false, "tls": tls, "skip-cert-verify": true,
		"network": firstNonEmpty(str("net"), "tcp"),
	}
	if tls {
		p["servername"] = firstNonEmpty(str("sni"), str("host"), str("add"))
		p["alpn"] = []string{"h2", "http/1.1"}
		p["fingerprint"] = str("fp")
		p["client-fingerprint"] = firstNonEmpty(str("fp"), "chrome")
		if str("security") == "reality" || str("pbk") != "" {
			p["reality-opts"] = map[string]any{
				"public-key": str("pbk"), "short-id": str("sid"),
			}
		}
	}
	switch str("net") {
	case "ws":
		p["ws-opts"] = map[string]any{
			"path": firstNonEmpty(str("path"), "/"), "headers": hostHeader(str("host")),
		}
	case "h2":
		p["h2-opts"] = map[string]any{
			"path": firstNonEmpty(str("path"), "/"),
			"host": []string{firstNonEmpty(str("host"), "")},
		}
	case "grpc":
		p["grpc-opts"] = map[string]any{"grpc-service-name": str("path")}
	case "http":
		p["network"] = "http"
		p["http-opts"] = map[string]any{
			"method": "GET", "path": []string{firstNonEmpty(str("path"), "/")},
			"headers": map[string]any{"Host": []string{firstNonEmpty(str("host"), "")}},
		}
	case "httpupgrade":
		p["network"] = "ws"
		p["ws-opts"] = map[string]any{
			"path": firstNonEmpty(str("path"), "/"), "headers": hostHeader(str("host")),
			"v2ray-http-upgrade": true,
		}
	case "xhttp":
		p["network"] = "xhttp"
		opts := map[string]any{"path": firstNonEmpty(str("path"), "/"), "mode": firstNonEmpty(str("mode"), "auto"), "extra": map[string]any{}}
		if str("host") != "" {
			opts["headers"] = map[string]any{"Host": str("host")}
		}
		p["xhttp-opts"] = opts
	case "quic":
		return nil
	}
	p["smux"] = map[string]any{"enabled": false}
	return p
}

func convertTrojan(config string) map[string]any {
	u, _, _, err := parseProxyURL(config)
	if err != nil {
		return nil
	}
	q := u.Query()
	host := u.Hostname()
	port := atoiOr(u.Port(), 443)
	name := fragmentName(u, fmt.Sprintf("Trojan-%s-%d", host, port))

	p := map[string]any{
		"name": name, "type": "trojan", "server": host, "port": port,
		"password": u.User.Username(), "udp": true,
		"sni": firstNonEmpty(q.Get("sni"), host), "alpn": []string{"h2", "http/1.1"},
		"client-fingerprint": firstNonEmpty(q.Get("fp"), "chrome"),
		"fingerprint":        q.Get("fp"), "skip-cert-verify": true,
		"ss-opts": map[string]any{"enabled": false, "method": "aes-128-gcm", "password": "example"},
		"network": "tcp", "smux": map[string]any{"enabled": false},
	}
	if q.Get("security") == "reality" || q.Get("pbk") != "" {
		p["reality-opts"] = map[string]any{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
	}
	switch q.Get("type") {
	case "ws":
		p["network"] = "ws"
		p["ws-opts"] = map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "headers": hostHeader(q.Get("host"))}
	case "grpc":
		p["network"] = "grpc"
		p["grpc-opts"] = map[string]any{"grpc-service-name": firstNonEmpty(q.Get("serviceName"), q.Get("path"))}
	case "http":
		p["network"] = "http"
		p["http-opts"] = map[string]any{
			"method": "GET", "path": []string{firstNonEmpty(q.Get("path"), "/")},
			"headers": map[string]any{"Host": []string{q.Get("host")}},
		}
	case "httpupgrade":
		p["network"] = "ws"
		p["ws-opts"] = map[string]any{
			"path": firstNonEmpty(q.Get("path"), "/"), "headers": hostHeader(q.Get("host")),
			"v2ray-http-upgrade": true,
		}
	case "quic":
		return nil
	}
	return p
}

func convertVless(config string) map[string]any {
	u, _, _, err := parseProxyURL(config)
	if err != nil {
		return nil
	}
	q := u.Query()
	host := u.Hostname()
	port := atoiOr(u.Port(), 443)
	name := fragmentName(u, fmt.Sprintf("VLESS-%s-%d", host, port))
	security := q.Get("security")
	isReality := security == "reality"
	isTLS := security == "tls" || isReality

	p := map[string]any{
		"name": name, "type": "vless", "server": host, "port": port,
		"uuid": u.User.Username(), "udp": true, "tls": isTLS, "skip-cert-verify": true,
		"servername": firstNonEmpty(q.Get("sni"), host), "flow": q.Get("flow"),
		"packet-encoding":    firstNonEmpty(q.Get("packet-encoding"), q.Get("packetEncoding"), "xudp"),
		"encryption":         firstNonEmpty(q.Get("encryption"), "none"),
		"client-fingerprint": firstNonEmpty(q.Get("fp"), "chrome"),
	}
	if stls := firstNonEmpty(q.Get("shadow-tls-password"), q.Get("shadow_tls_password"), q.Get("stls-password")); stls != "" {
		p["shadow-tls-opts"] = map[string]any{
			"version":  atoiOr(firstNonEmpty(q.Get("shadow-tls-version"), q.Get("stls-version"), "1"), 1),
			"password": stls,
		}
	}
	if isReality || q.Get("pbk") != "" {
		p["reality-opts"] = map[string]any{"public-key": q.Get("pbk"), "short-id": q.Get("sid")}
	}

	net := firstNonEmpty(q.Get("type"), "tcp")
	p["network"] = net
	switch net {
	case "ws":
		p["ws-opts"] = map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "headers": hostHeader(q.Get("host"))}
	case "grpc":
		p["grpc-opts"] = map[string]any{"grpc-service-name": firstNonEmpty(q.Get("serviceName"), q.Get("path"))}
	case "xhttp":
		opts := map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "mode": firstNonEmpty(q.Get("mode"), "auto"), "extra": map[string]any{}}
		if q.Get("host") != "" {
			opts["headers"] = map[string]any{"Host": q.Get("host")}
		}
		p["xhttp-opts"] = opts
	case "h2":
		p["h2-opts"] = map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "host": []string{q.Get("host")}}
	case "http":
		p["http-opts"] = map[string]any{
			"method": "GET", "path": []string{firstNonEmpty(q.Get("path"), "/")},
			"headers": map[string]any{"Host": []string{q.Get("host")}},
		}
	case "httpupgrade":
		p["network"] = "ws"
		p["ws-opts"] = map[string]any{
			"path": firstNonEmpty(q.Get("path"), "/"), "headers": hostHeader(q.Get("host")),
			"v2ray-http-upgrade": true,
		}
	case "quic":
		return nil
	}
	return p
}

func convertShadowsocks(config string) map[string]any {
	parsed, err := parseShadowsocksURI(config)
	if err != nil {
		return nil
	}
	p := map[string]any{
		"name": parsed.Name, "type": "ss", "server": parsed.Server, "port": parsed.Port,
		"cipher": parsed.Method, "password": parsed.Password, "udp": true,
		"udp-over-tcp": false, "udp-over-tcp-version": 2, "ip-version": "ipv4",
		"smux": map[string]any{"enabled": false},
	}
	info := parsePluginOptions(parsed.Query.Get("plugin"), firstNonEmpty(parsed.Query.Get("plugin-opts"), parsed.Query.Get("plugin_opts")))
	if info.Plugin != "" {
		p["plugin"] = info.Plugin
		if len(info.Opts) > 0 {
			p["plugin-opts"] = info.Opts
		}
		if info.ClientFingerprint != "" {
			p["client-fingerprint"] = info.ClientFingerprint
		}
	}
	return p
}

func convertHysteria2(config string) map[string]any {
	u, portRange, firstPort, err := parseProxyURL(config)
	if err != nil {
		return nil
	}
	q := u.Query()
	host := u.Hostname()
	basePort := firstPort
	if basePort == 0 {
		basePort = atoiOr(u.Port(), 443)
	}
	name := fragmentName(u, fmt.Sprintf("Hysteria2-%s-%d", host, basePort))

	password := ""
	if pw, ok := u.User.Password(); ok {
		password = safeDecode(u.User.Username() + ":" + pw)
	} else {
		password = safeDecode(u.User.Username())
	}

	p := map[string]any{
		"name": name, "type": "hysteria2", "server": host, "port": basePort,
		"password": password, "sni": firstNonEmpty(q.Get("sni"), host),
		"skip-cert-verify": q.Get("insecure") == "1",
		"alpn":             splitCSV(firstNonEmpty(q.Get("alpn"), "h3")),
	}
	portsRange := firstNonEmpty(q.Get("ports"), q.Get("mport"), portRange)
	if portsRange != "" {
		p["ports"] = portsRange
		if hi := firstNonEmpty(q.Get("hop-interval"), q.Get("hop_interval"), q.Get("hopInterval")); hi != "" {
			p["hop-interval"] = atoiOr(hi, 0)
		}
	}
	if fp := firstNonEmpty(q.Get("pinSHA256"), q.Get("fp")); fp != "" {
		p["fingerprint"] = fp
	}
	if up := firstNonEmpty(q.Get("up"), q.Get("upspeed")); up != "" {
		if strings.Contains(strings.ToLower(up), "mbps") {
			p["up"] = up
		} else {
			p["up"] = up + " Mbps"
		}
	}
	if down := firstNonEmpty(q.Get("down"), q.Get("downspeed")); down != "" {
		if strings.Contains(strings.ToLower(down), "mbps") {
			p["down"] = down
		} else {
			p["down"] = down + " Mbps"
		}
	}
	if obfs := q.Get("obfs"); obfs != "" && obfs != "none" {
		p["obfs"] = obfs
		p["obfs-password"] = firstNonEmpty(q.Get("obfs-password"), q.Get("obfsParam"))
	}
	return p
}

func convertTuic(config string) map[string]any {
	u, _, _, err := parseProxyURL(config)
	if err != nil {
		return nil
	}
	q := u.Query()
	host := u.Hostname()
	port := atoiOr(u.Port(), 443)
	name := fragmentName(u, fmt.Sprintf("Tuic-%s-%d", host, port))

	uuid := u.User.Username()
	password, _ := u.User.Password()
	if strings.Contains(uuid, ":") {
		parts := strings.SplitN(uuid, ":", 2)
		uuid, password = parts[0], parts[1]
	}
	p := map[string]any{
		"name": name, "type": "tuic", "server": host, "port": port,
		"uuid": uuid, "password": password, "token": q.Get("token"),
		"sni": firstNonEmpty(q.Get("sni"), host), "skip-cert-verify": q.Get("insecure") == "1",
		"alpn":        splitCSV(firstNonEmpty(q.Get("alpn"), "h3")),
		"disable-sni": q.Get("disable-sni") == "true", "reduce-rtt": true,
		"request-timeout":       8000,
		"udp-relay-mode":        firstNonEmpty(q.Get("udp-relay-mode"), q.Get("udp_relay_mode"), "native"),
		"congestion-controller": firstNonEmpty(q.Get("congestion_control"), q.Get("congestion-controller"), q.Get("congestion_controller"), "bbr"),
	}
	if fo := q.Get("fast-open"); fo != "" {
		p["fast-open"] = fo == "true" || fo == "1"
	}
	return p
}

func convertAnyTLS(config string) map[string]any {
	u, _, _, err := parseProxyURL(config)
	if err != nil {
		return nil
	}
	q := u.Query()
	host := u.Hostname()
	port := atoiOr(u.Port(), 443)
	name := fragmentName(u, fmt.Sprintf("AnyTLS-%s-%d", host, port))
	p := map[string]any{
		"name": name, "type": "anytls", "server": host, "port": port,
		"password": u.User.Username(), "client-fingerprint": firstNonEmpty(q.Get("fp"), "chrome"),
		"udp": true, "idle-session-check-interval": 30, "idle-session-timeout": 30,
		"min-idle-session": 0, "sni": firstNonEmpty(q.Get("sni"), q.Get("host"), host),
		"alpn": splitCSV(firstNonEmpty(q.Get("alpn"), "h2,http/1.1")), "skip-cert-verify": true,
	}
	switch q.Get("type") {
	case "ws":
		p["network"] = "ws"
		p["ws-opts"] = map[string]any{
			"path":    firstNonEmpty(q.Get("path"), "/"),
			"headers": hostHeader(firstNonEmpty(q.Get("host"), fmt.Sprint(p["sni"]))),
		}
	case "grpc":
		p["network"] = "grpc"
		p["grpc-opts"] = map[string]any{"grpc-service-name": firstNonEmpty(q.Get("serviceName"), q.Get("path"))}
	}
	return p
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hostHeader(host string) map[string]any {
	if host == "" {
		return map[string]any{}
	}
	return map[string]any{"Host": host}
}

func firstNonNil(v any, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

// ---------------- 生成与注入 ----------------

type clashResult struct {
	content     string
	templateURL string
	total       int
	skipped     int
	skippedBy   map[string]int
	reasons     map[string]string
	protocols   map[string]int
}

func (h *handlers) buildClashProxies(ctx context.Context, links []string) ([]map[string]any, map[string]int, map[string]int, map[string]string) {
	proxies := []map[string]any{{"name": "直连", "type": "direct"}}
	skippedBy := map[string]int{}
	reasons := map[string]string{}
	protocols := map[string]int{}

	for _, link := range links {
		protocol := getProtocol(link)
		converted := convertToClashFormat(link)
		if converted == nil {
			skippedBy[protocol]++
			if reasons[protocol] == "" {
				reasons[protocol] = clashUnsupportedReason(link, protocol)
			}
			continue
		}
		proxies = append(proxies, converted)
	}
	// 按规范 JSON 去重（键序无关）
	seen := map[string]bool{}
	unique := proxies[:0]
	for _, p := range proxies {
		key := canonicalJSON(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, p)
	}
	proxies = unique

	names := map[string]bool{}
	for _, p := range proxies {
		base := fmt.Sprint(p["name"])
		name := base
		counter := 1
		for names[name] {
			name = fmt.Sprintf("%s-%d", base, counter)
			counter++
		}
		p["name"] = name
		names[name] = true
		if t := fmt.Sprint(p["type"]); t != "direct" {
			protocols[t]++
		}
	}
	return proxies, skippedBy, protocols, reasons
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

var proxiesLineRe = regexp.MustCompile(`(?m)^proxies:[ \t\r]*$`)
var blankBlockRe = regexp.MustCompile(`\n\s*\n`)

// injectClashProxies 复刻 Worker 的模板注入逻辑（保留缩进与空行）。
func injectClashProxies(templateContent string, proxies []map[string]any) string {
	loc := proxiesLineRe.FindStringIndex(templateContent)
	if loc == nil {
		var b strings.Builder
		b.WriteString(templateContent)
		b.WriteString("\n\n# ===== 生成的节点配置 =====\nproxies:\n")
		for i, p := range proxies {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString("  - " + canonicalJSON(p))
		}
		return b.String()
	}
	// "proxies:" 之后的第一个换行
	insertPos := strings.Index(templateContent[loc[1]:], "\n")
	if insertPos < 0 {
		return templateContent
	}
	insertPos += loc[1]

	end := len(templateContent)
	if m := blankBlockRe.FindStringIndex(templateContent[insertPos:]); m != nil {
		end = insertPos + m[1]
	}

	var b strings.Builder
	b.WriteString(templateContent[:insertPos+1])
	for i, p := range proxies {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  - " + canonicalJSON(p))
	}
	b.WriteString("\n")
	b.WriteString(templateContent[end:])
	return b.String()
}

// GET /clash-config?uuid=&template=
func (h *handlers) clashConfig(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	uuid := strings.TrimSpace(q.Get("uuid"))
	template := strings.TrimSpace(q.Get("template"))
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "缺少必要参数: uuid")
		return
	}
	if template == "" {
		writeError(w, http.StatusBadRequest, "缺少必要参数: template")
		return
	}
	if !h.subscribeAllowed(w, r, uuid) {
		return
	}

	links, extSummary, err := h.subscriptionLines(r.Context(), uuid)
	if err != nil {
		h.log.Error("读取订阅配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "生成 Clash 配置失败")
		return
	}
	if len(links) == 0 {
		writeError(w, http.StatusBadRequest, "未能生成有效的节点配置（本地配置与外部订阅源均为空）")
		return
	}

	proxies, skippedBy, protocols, reasons := h.buildClashProxies(r.Context(), links)
	if len(proxies) <= 1 {
		writeError(w, http.StatusBadRequest, "未能生成有效的代理节点")
		return
	}

	templateURL := template
	if !strings.HasPrefix(template, "http://") && !strings.HasPrefix(template, "https://") {
		err := h.db.QueryRowContext(r.Context(),
			`SELECT url FROM config_templates WHERE name = ? LIMIT 1`, template).Scan(&templateURL)
		if err != nil {
			if err == sql.ErrNoRows {
				writeError(w, http.StatusNotFound, "未在数据库中找到模板 \""+template+"\"")
			} else {
				writeError(w, http.StatusInternalServerError, "查询模板失败")
			}
			return
		}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(templateURL)
	if err != nil {
		writeError(w, http.StatusBadGateway, "模板载入失败: "+err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("模板载入失败: HTTP %d", resp.StatusCode))
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		writeError(w, http.StatusBadGateway, "模板载入失败: 读取失败")
		return
	}
	if strings.TrimSpace(string(body)) == "" {
		writeError(w, http.StatusBadGateway, "模板载入失败: 内容为空")
		return
	}

	finalConfig := injectClashProxies(string(body), proxies)

	queryType := "subscription"
	if ref := r.Header.Get("Referer"); ref != "" && strings.Contains(ref, requestOrigin(r)) {
		queryType = "api-generation"
	}
	h.logAccess(r, uuid, queryType)

	skippedCount := 0
	for _, n := range skippedBy {
		skippedCount += n
	}
	skippedSummary := summaryPairs(skippedBy)
	skippedDetails := make([]map[string]any, 0, len(skippedBy))
	for proto, count := range skippedBy {
		skippedDetails = append(skippedDetails, map[string]any{
			"protocol": proto, "count": count, "reason": reasons[proto],
		})
	}
	detailsJSON, _ := json.Marshal(skippedDetails)

	w.Header().Set("Content-Type", "text/yaml;charset=UTF-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="clash-config-%s-%s.yaml"`,
		uuid, time.Now().UTC().Format("2006-01-02")))
	w.Header().Set("X-Generated-Date", time.Now().UTC().Format(time.RFC3339))
	w.Header().Set("X-Total-Proxies-Unique", strconv.Itoa(len(proxies)))
	w.Header().Set("X-Template-Used", templateURL)
	w.Header().Set("X-Supported-Protocols", strings.Join(mapKeys(protocols), ","))
	w.Header().Set("X-Protocol-Counts", summaryPairs(protocols))
	w.Header().Set("X-Skipped-Configs", strconv.Itoa(skippedCount))
	w.Header().Set("X-Skipped-Protocols", skippedSummary)
	w.Header().Set("X-Approximated-Protocols", "")
	w.Header().Set("X-Skipped-Details", base64.StdEncoding.EncodeToString(detailsJSON))
	w.Header().Set("X-External-Sources", strconv.Itoa(extSummary.Sources))
	w.Header().Set("X-External-Nodes", strconv.Itoa(extSummary.Added))
	w.Header().Set("X-External-Errors", strconv.Itoa(extSummary.Errors))
	w.Header().Set("X-External-Unsupported", strconv.Itoa(extSummary.Unsupported))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(finalConfig))
}

func summaryPairs(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// 稳定输出
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", k, m[k]))
	}
	return strings.Join(parts, ",")
}

func mapKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func clashUnsupportedReason(config, protocol string) string {
	lower := strings.ToLower(config)
	if protocol == "shadowtls" {
		return "独立 ShadowTLS 链接（请改用 ss:// 或 snell:// 的 shadow-tls 插件形式）"
	}
	if protocol == "unknown" {
		return "无法识别的协议"
	}
	switch protocol {
	case "vmess", "trojan", "vless", "any-reality", "reality", "anyreality":
		if regexp.MustCompile(`[?&](type|net)=quic(&|#|$)`).MatchString(lower) {
			return "mihomo 不支持 QUIC 传输"
		}
		return ""
	case "shadowsocks", "hysteria2", "tuic", "anytls":
		return ""
	}
	return protocol + " 节点解析失败（不支持的参数或传输）"
}
