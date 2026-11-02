package api

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
)

func TestHashEmailCodeMatchesWorker(t *testing.T) {
	// 与 worker.js 的 hashEmailCode 使用同一算法：hex(sha256("purpose:email:code"))
	got := hashEmailCode("user@example.com", "123456", "portal_login")
	want := "cc1ac3fefaea24e11227f65dfde2162cdcb420833325dfc89a2c0d69a0424007"
	if got != want {
		t.Fatalf("hash = %s，期望 %s", got, want)
	}
}

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"  User@Example.COM ": "user@example.com",
		"a@b":                 "",
		"":                    "",
		"a b@c.com":           "",
	}
	for in, want := range cases {
		if got := normalizeEmail(in); got != want {
			t.Errorf("normalizeEmail(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestParseSubscriptionBodyPlainAndBase64(t *testing.T) {
	plain := "vmess://aaa\nss://bbb\n# comment\n\nvless://ccc\r\n"
	got, unsupported := parseSubscriptionBody(plain)
	if len(got) != 3 || got[0] != "vmess://aaa" || got[2] != "vless://ccc" {
		t.Fatalf("明文解析结果 = %#v", got)
	}
	if unsupported != 0 {
		t.Fatalf("unsupported = %d", unsupported)
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(plain))
	got, _ = parseSubscriptionBody(encoded)
	if len(got) != 3 {
		t.Fatalf("base64 解析结果 = %#v", got)
	}
}

func TestParseSubscriptionBodyIgnoresNonProxyLines(t *testing.T) {
	body := "http://example.com\nhello\nhysteria2://x\n"
	got, unsupported := parseSubscriptionBody(body)
	if len(got) != 1 || got[0] != "hysteria2://x" {
		t.Fatalf("结果 = %#v", got)
	}
	if unsupported != 1 {
		t.Fatalf("unsupported = %d，期望 1（http:// 不是受支持的代理协议）", unsupported)
	}
}

func TestNormalizeUuidStatus(t *testing.T) {
	if got := normalizeUuidStatus("SUSPENDED"); got != "suspended" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeUuidStatus("bogus"); got != "active" {
		t.Fatalf("got %q", got)
	}
}

func TestAtoiDefault(t *testing.T) {
	if got := atoiDefault("42", 1); got != 42 {
		t.Fatalf("got %d", got)
	}
	if got := atoiDefault("-1", 7); got != 7 {
		t.Fatalf("got %d", got)
	}
	if got := atoiDefault("abc", 3); got != 3 {
		t.Fatalf("got %d", got)
	}
}

func TestRequestOriginHonoursForwardedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "http://internal:8080/api/portal/uuids", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-Host", "pp.example.com")
	if got := requestOrigin(r); got != "https://pp.example.com" {
		t.Fatalf("origin = %q", got)
	}
}

func TestLegacyPath(t *testing.T) {
	for _, p := range []string{"/api/uuids", "/sub/x", "/clash-config", "/manage/configs"} {
		if !legacyPath(p) {
			t.Errorf("%s 应可转发", p)
		}
	}
	for _, p := range []string{"/admin", "/login", "/healthz"} {
		if legacyPath(p) {
			t.Errorf("%s 不应转发", p)
		}
	}
}
