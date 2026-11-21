package api

import "testing"

func TestNormalizeSingboxComponentBase(t *testing.T) {
	ok := []string{
		"https://s3.proxypilot.yangzifun.org/singbox",
		"https://s3.proxypilot.yangzifun.org/singbox/",
		"  https://s3.proxypilot.yangzifun.org/singbox  ",
		"",
	}
	for _, raw := range ok {
		got, err := normalizeSingboxComponentBase(raw)
		if err != nil || got != singboxComponentBase {
			t.Fatalf("normalize(%q) = %q, %v", raw, got, err)
		}
	}
	bad := []string{
		"http://s3.proxypilot.yangzifun.org/singbox",
		"https://github.com/SagerNet/sing-box/releases/download",
		"https://evil.example.com/singbox",
		"https://s3.proxypilot.yangzifun.org/other",
		"https://s3.proxypilot.yangzifun.org/singbox/../other",
		"https://user:pass@s3.proxypilot.yangzifun.org/singbox",
		"https://s3.proxypilot.yangzifun.org/singbox?x=1",
		"ftp://s3.proxypilot.yangzifun.org/singbox",
	}
	for _, raw := range bad {
		if _, err := normalizeSingboxComponentBase(raw); err == nil {
			t.Fatalf("normalize(%q) 应拒绝", raw)
		}
		if err := validateSingboxComponentBase(raw); err == nil {
			t.Fatalf("validate(%q) 应拒绝", raw)
		}
	}
}

func TestComponentBinaryURLOnlyAllowsS3(t *testing.T) {
	sha := "206e0bee883820dec2736d70a2d808b38d50f092fe46baec7adda8bbdba3d065"
	got := componentBinaryURL(singboxComponentBase, sha, "amd64")
	want := "https://s3.proxypilot.yangzifun.org/singbox/sing-box-linux-amd64"
	if got != want {
		t.Fatalf("componentBinaryURL = %q, want %q", got, want)
	}
	if got := componentBinaryURL("https://github.com/SagerNet/sing-box", sha, "amd64"); got != "" {
		t.Fatalf("GitHub 来源应被拒绝，got %q", got)
	}
	if got := componentBinaryURL("https://evil.example.com/singbox", sha, "arm64"); got != "" {
		t.Fatalf("第三方来源应被拒绝，got %q", got)
	}
	if got := componentBinaryURL("http://s3.proxypilot.yangzifun.org/singbox", sha, "amd64"); got != "" {
		t.Fatalf("http 来源应被拒绝，got %q", got)
	}
	if got := componentBinaryURL(singboxComponentBase, "", "amd64"); got != "" {
		t.Fatalf("缺 sha 应 fail-closed，got %q", got)
	}
	if got := componentBinaryURL("", sha, "amd64"); got != "" {
		t.Fatalf("空基址应 fail-closed，got %q", got)
	}
}

func TestIsAllowedSingboxComponentURL(t *testing.T) {
	allowed := []string{
		"https://s3.proxypilot.yangzifun.org/singbox/sing-box-linux-amd64",
		"https://s3.proxypilot.yangzifun.org/singbox/sing-box-linux-arm64",
	}
	for _, u := range allowed {
		if !isAllowedSingboxComponentURL(u) {
			t.Fatalf("%q 应允许", u)
		}
	}
	denied := []string{
		"https://github.com/SagerNet/sing-box/releases/download/v1.14.4/x",
		"https://s3.proxypilot.yangzifun.org/agent-dist/x",
		"http://s3.proxypilot.yangzifun.org/singbox/x",
		"https://s3.proxypilot.yangzifun.org/singbox/../agent-dist/x",
		"https://s3.proxypilot.yangzifun.org/singbox/%2e%2e/agent-dist/x",
		"",
	}
	for _, u := range denied {
		if isAllowedSingboxComponentURL(u) {
			t.Fatalf("%q 应拒绝", u)
		}
	}
}
