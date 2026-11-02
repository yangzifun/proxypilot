package api

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

type protocolPattern struct {
	prefix string
	typ    string
}

// 与 worker.js 的 protocolPatterns 保持一致（顺序敏感）。
var protocolPatterns = []protocolPattern{
	{"naive+https://", "naive"},
	{"naive+quic://", "naive"},
	{"naive://", "naive"},
	{"vless://", "vless"},
	{"vmess://", "vmess"},
	{"trojan://", "trojan"},
	{"ssr://", "shadowsocksr"},
	{"ss://", "shadowsocks"},
	{"hysteria2://", "hysteria2"},
	{"hy2://", "hysteria2"},
	{"hysteria://", "hysteria"},
	{"hy://", "hysteria"},
	{"tuic://", "tuic"},
	{"anytls://", "anytls"},
	{"any-reality://", "any-reality"},
	{"anyreality://", "anyreality"},
	{"reality://", "reality"},
	{"shadowtls://", "shadowtls"},
	{"shadow-tls://", "shadowtls"},
	{"snell://", "snell"},
	{"wireguard://", "wireguard"},
	{"wg://", "wireguard"},
	{"ssh://", "ssh"},
	{"socks4://", "socks4"},
	{"socks5://", "socks5"},
	{"socks://", "socks5"},
	{"mieru://", "mieru"},
	{"sudoku://", "sudoku"},
	{"shadowquic://", "shadowquic"},
	{"https://", "http"},
	{"http://", "http"},
}

var urlBasedProtocols = map[string]bool{
	"vless": true, "trojan": true, "hysteria": true, "hysteria2": true, "tuic": true,
	"anytls": true, "any-reality": true, "anyreality": true, "reality": true,
	"socks4": true, "socks5": true, "naive": true, "http": true, "ssh": true,
	"snell": true, "wireguard": true, "shadowtls": true, "mieru": true,
	"sudoku": true, "shadowquic": true,
}

func getProtocol(config string) string {
	lower := strings.ToLower(strings.TrimSpace(config))
	for _, p := range protocolPatterns {
		if strings.HasPrefix(lower, p.prefix) {
			return p.typ
		}
	}
	return "unknown"
}

var ssrRemarksRe = regexp.MustCompile(`remarks=([A-Za-z0-9\-_+/=]+)`)

func extractRemark(config, protocol string) string {
	switch protocol {
	case "vmess":
		raw := strings.TrimSpace(config)
		if !strings.HasPrefix(strings.ToLower(raw), "vmess://") {
			return ""
		}
		decoded, ok := decodeAnyBase64(raw[8:])
		if !ok {
			return ""
		}
		var obj struct {
			PS     string `json:"ps"`
			Remark string `json:"remark"`
		}
		if err := json.Unmarshal(decoded, &obj); err != nil {
			return ""
		}
		if obj.PS != "" {
			return obj.PS
		}
		return obj.Remark
	case "shadowsocksr":
		decoded, ok := decodeAnyBase64(strings.TrimPrefix(strings.TrimSpace(config), "ssr://"))
		if !ok {
			return ""
		}
		if m := ssrRemarksRe.FindSubmatch(decoded); len(m) == 2 {
			if b, ok := decodeAnyBase64(string(m[1])); ok {
				return string(b)
			}
		}
		return ""
	case "shadowsocks":
		if i := strings.Index(config, "#"); i >= 0 {
			if r, err := url.QueryUnescape(config[i+1:]); err == nil {
				return r
			}
			return config[i+1:]
		}
		return ""
	}
	if urlBasedProtocols[protocol] {
		if i := strings.Index(config, "#"); i >= 0 {
			if r, err := url.QueryUnescape(config[i+1:]); err == nil {
				return r
			}
			return config[i+1:]
		}
	}
	return ""
}

func decodeAnyBase64(raw string) ([]byte, bool) {
	s := strings.TrimSpace(raw)
	encs := []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	}
	for _, enc := range encs {
		if b, err := enc.DecodeString(s); err == nil {
			return b, true
		}
	}
	return nil, false
}
