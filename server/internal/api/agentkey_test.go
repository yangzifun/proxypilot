package api

import (
	"context"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

func TestAPIKeyPermissionMapping(t *testing.T) {
	cases := []struct {
		method, path, want string
	}{
		{"GET", "/api/uuids", "groups:read"},
		{"POST", "/api/uuids", "groups:write"},
		{"PUT", "/api/uuids", "groups:write"},
		{"DELETE", "/api/uuids", "groups:write"},
		{"GET", "/api/uuids/abc/configs", "configs:read"},
		{"GET", "/api/templates/manage", "templates:read"},
		{"POST", "/api/templates/manage", "templates:write"},
		{"GET", "/api/servers", ""},
		{"GET", "/api/portal/uuids", ""},
	}
	for _, c := range cases {
		if got := apiKeyPermission(c.path, c.method); got != c.want {
			t.Errorf("apiKeyPermission(%s %s) = %q，期望 %q", c.method, c.path, got, c.want)
		}
	}
}

func TestAPIKeyPermissionCheck(t *testing.T) {
	cases := []struct {
		perms, required string
		want            bool
	}{
		{"*", "groups:write", true},
		{"groups", "groups:read", true},
		{"groups:read", "groups:read", true},
		{"groups:read", "groups:write", false},
		{"configs:write,groups:read", "groups:read", true},
		{"", "groups:read", false},
	}
	for _, c := range cases {
		if got := apiKeyHasPermission(c.perms, c.required); got != c.want {
			t.Errorf("apiKeyHasPermission(%q, %q) = %v，期望 %v", c.perms, c.required, got, c.want)
		}
	}
}

func TestAgentTokenFormatAndHint(t *testing.T) {
	token, err := generateAgentToken()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "ag_") || len(token) != 3+64 {
		t.Fatalf("token = %q", token)
	}
	hint := agentTokenHint(token)
	if !strings.HasPrefix(hint, token[:7]) || !strings.HasSuffix(hint, token[len(token)-4:]) {
		t.Fatalf("hint = %q", hint)
	}
	if !strings.Contains(hint, "****") {
		t.Fatalf("hint 缺少掩码: %q", hint)
	}
}

func TestAgentTokenEncryptRoundTrip(t *testing.T) {
	secret := "test-agent-key-secret"
	token := "ag_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	enc, err := encryptAgentToken(secret, token)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enc, "enc:v1:") {
		t.Fatalf("密文格式 = %q", enc)
	}
	if got := decryptAgentToken(secret, enc); got != token {
		t.Fatalf("解密 = %q", got)
	}
	if got := decryptAgentToken("other-secret", enc); got != "" {
		t.Fatal("错误密钥不应解出明文")
	}
	if got := decryptAgentToken(secret, "garbage"); got != "" {
		t.Fatal("非法密文应返回空串")
	}
}

func TestAgentInstallCommand(t *testing.T) {
	h := &handlers{cfg: config.Config{}}
	got := h.agentInstallCommand(context.Background(), "https://pp.example.com", "ag_abc")
	want := "curl -fsSL https://pp.example.com/api/agent/install.sh | sudo sh -s -- --key ag_abc"
	if got != want {
		t.Fatalf("install = %q，期望 %q", got, want)
	}

	h.cfg.AgentInstallBase = "https://s3.example.com/agent/"
	got = h.agentInstallCommand(context.Background(), "https://pp.example.com", "ag_abc")
	want = "curl -fsSL https://s3.example.com/agent/install.sh | sudo sh -s -- --key ag_abc --url https://pp.example.com"
	if got != want {
		t.Fatalf("install(base) = %q，期望 %q", got, want)
	}
}

func TestServerInputNormalize(t *testing.T) {
	name := "  日本-01  "
	host := "https://Example.COM:8443/path"
	tags := " jp "
	poll := int64(1)
	in := serverInput{Name: &name, Host: &host, Tags: &tags, AgentNextPollSec: &poll}
	if err := in.normalize(); err != nil {
		t.Fatal(err)
	}
	if *in.Name != "日本-01" {
		t.Fatalf("name = %q", *in.Name)
	}
	if *in.Host != "Example.COM" {
		t.Fatalf("host = %q", *in.Host)
	}
	if *in.Tags != "jp" {
		t.Fatalf("tags = %q", *in.Tags)
	}
	if *in.AgentNextPollSec != 2 {
		t.Fatalf("poll = %d，期望被收敛到 2", *in.AgentNextPollSec)
	}

	badHost := "http://a b.com"
	in2 := serverInput{Host: &badHost}
	if err := in2.normalize(); err == nil {
		t.Fatal("非法主机名应报错")
	}
}
