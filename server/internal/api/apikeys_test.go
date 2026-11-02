package api

import (
	"strings"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

func TestAPIKeyPermissionsNormalize(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"groups:read,configs:write", "groups:read,configs:write"},
		{"GROUPS:READ", "groups:read"},
		{"groups:read,bogus,groups:read", "groups:read"},
		{"*", "*"},
		{[]any{"templates:write", "bogus"}, "templates:write"},
		{"", ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := normalizeAPIKeyPermissions(c.in); got != c.want {
			t.Errorf("normalize(%v) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestAPIKeyDescribe(t *testing.T) {
	if got := describeAPIKeyPermissions("groups:read,configs:write"); got != "分组只读、配置管理" {
		t.Fatalf("got %q", got)
	}
	if got := describeAPIKeyPermissions("*"); got != "全部权限" {
		t.Fatalf("got %q", got)
	}
	if got := describeAPIKeyPermissions(""); got != "无权限" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveAPIKeyExpiry(t *testing.T) {
	if v, msg := resolveAPIKeyExpiry(nil, nil); msg != "" || v != 0 {
		t.Fatalf("v=%d msg=%q", v, msg)
	}
	if v, msg := resolveAPIKeyExpiry(float64(0), nil); msg != "" || v != 0 {
		t.Fatalf("0 天应为永不过期: v=%d msg=%q", v, msg)
	}
	v, msg := resolveAPIKeyExpiry(float64(10), nil)
	if msg != "" {
		t.Fatalf("msg=%q", msg)
	}
	if v < time.Now().Add(9*24*time.Hour).UnixMilli() || v > time.Now().Add(11*24*time.Hour).UnixMilli() {
		t.Fatalf("10 天的过期时间不合理: %d", v)
	}
	if _, msg := resolveAPIKeyExpiry("-1", nil); msg == "" {
		t.Fatal("负数应报错")
	}
	if _, msg := resolveAPIKeyExpiry(nil, "abc"); msg == "" {
		t.Fatal("非法时间戳应报错")
	}
}

func TestAPIKeyFormat(t *testing.T) {
	key, err := generateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "pk_") || len(key) != 3+64 {
		t.Fatalf("key = %q", key)
	}
	hint := apiKeyHint(key)
	if !strings.Contains(hint, "****") || !strings.HasSuffix(hint, key[len(key)-4:]) {
		t.Fatalf("hint = %q", hint)
	}
}

func TestGenerateBackupCodes(t *testing.T) {
	codes, err := generateBackupCodes(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("数量 = %d", len(codes))
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != 8 || strings.ToUpper(c) != c {
			t.Fatalf("备份码格式不对: %q", c)
		}
		if seen[c] {
			t.Fatalf("备份码重复: %q", c)
		}
		seen[c] = true
	}
}

func TestTotpSecretAndURL(t *testing.T) {
	secret, err := auth.GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 20 {
		t.Fatalf("secret = %q", secret)
	}
	link := auth.OTPAuthURL("admin", secret, "YZFN Admin")
	if !strings.HasPrefix(link, "otpauth://totp/") || !strings.Contains(link, secret) {
		t.Fatalf("otpauth = %q", link)
	}
}
