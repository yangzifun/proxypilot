package api

import (
	"encoding/base64"
	"testing"
)

func TestGetProtocol(t *testing.T) {
	cases := map[string]string{
		"vless://abc@example.com:443":         "vless",
		"VMESS://eyJhZGQiOiJ4In0=":            "vmess",
		"hy2://x@y:443":                       "hysteria2",
		"hysteria2://x@y:443":                 "hysteria2",
		"socks://x":                           "socks5",
		"ssr://abc":                           "shadowsocksr",
		"ss://YWVzLTEyOC1nY206cGFzcw==@h:443": "shadowsocks",
		"https://example.com":                 "http",
		"wireguard://x":                       "wireguard",
		"random-text":                         "unknown",
		"":                                    "unknown",
	}
	for in, want := range cases {
		if got := getProtocol(in); got != want {
			t.Errorf("getProtocol(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestExtractRemark(t *testing.T) {
	// vmess: base64(JSON).ps
	payload := `{"v":"2","ps":"香港 01","add":"example.com","port":"443"}`
	vmess := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))
	if got := extractRemark(vmess, "vmess"); got != "香港 01" {
		t.Fatalf("vmess remark = %q", got)
	}

	// ss: 片段
	ss := "ss://YWVzLTEyOC1nY206cGFzcw==@example.com:443#%E6%97%A5%E6%9C%AC%2001"
	if got := extractRemark(ss, "shadowsocks"); got != "日本 01" {
		t.Fatalf("ss remark = %q", got)
	}

	// vless: 片段
	vless := "vless://uuid@example.com:443?security=reality#US%20West"
	if got := extractRemark(vless, "vless"); got != "US West" {
		t.Fatalf("vless remark = %q", got)
	}

	// 无片段
	if got := extractRemark("vless://uuid@example.com:443", "vless"); got != "" {
		t.Fatalf("期望空 remark，得到 %q", got)
	}
}

func TestValidSubSourceURL(t *testing.T) {
	ok := []string{
		"https://example.com/sub/abc",
		"http://1.1.1.1:8080/sub",
	}
	for _, u := range ok {
		if _, valid := validSubSourceURL(u); !valid {
			t.Errorf("%q 应通过", u)
		}
	}
	bad := []string{
		"http://localhost:8080/sub",
		"http://127.0.0.1/sub",
		"http://10.1.2.3/sub",
		"http://192.168.1.1/sub",
		"http://172.16.0.1/sub",
		"ftp://example.com/sub",
		"not-a-url",
		"",
	}
	for _, u := range bad {
		if _, valid := validSubSourceURL(u); valid {
			t.Errorf("%q 应被拒绝", u)
		}
	}
}
