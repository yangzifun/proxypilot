package auth

import (
	"strings"
	"testing"
	"time"
)

func TestJWTRoundTrip(t *testing.T) {
	tok, err := Sign(Claims{Sub: "admin", Exp: time.Now().Add(time.Hour).UnixMilli()}, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Parse(tok, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if c.Sub != "admin" || c.Role != "" {
		t.Fatalf("claims = %+v", c)
	}
}

func TestJWTRejectsTamperedAndExpired(t *testing.T) {
	tok, _ := Sign(Claims{Sub: "admin", Exp: time.Now().Add(time.Hour).UnixMilli()}, "test-secret")
	if _, err := Parse(tok, "other-secret"); err == nil {
		t.Fatal("错误密钥应拒绝")
	}
	parts := strings.Split(tok, ".")
	parts[1] = "eyJzdWIiOiJhdHRhY2tlciIsImV4cCI6OTk5OTk5OTk5OTk5OX0"
	if _, err := Parse(strings.Join(parts, "."), "test-secret"); err == nil {
		t.Fatal("篡改载荷应拒绝")
	}
	expired, _ := Sign(Claims{Sub: "admin", Exp: time.Now().Add(-time.Minute).UnixMilli()}, "test-secret")
	if _, err := Parse(expired, "test-secret"); err == nil {
		t.Fatal("过期 token 应拒绝")
	}
}

func TestTOTPRFC6238Vector(t *testing.T) {
	// RFC 6238 附录 B：secret = "12345678901234567890"，T=59 时 TOTP-SHA1 为 94287082
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	at := time.Unix(59, 0)
	if !verifyTOTPAt(secret, "287082", at, 0) {
		t.Fatal("RFC 向量校验失败")
	}
	if verifyTOTPAt(secret, "287083", at, 0) {
		t.Fatal("错误验证码不应通过")
	}
	if !verifyTOTPAt(secret, "287082", at.Add(30*time.Second), 1) {
		t.Fatal("skew=1 应接受上一窗口")
	}
}

func TestIssueMFATokenPurpose(t *testing.T) {
	token, err := IssueMFAToken("admin", "secret")
	if err != nil || token == "" {
		t.Fatalf("签发失败: %v", err)
	}
	c, err := Parse(token, "secret")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if c.Sub != "admin" || c.Purpose != "mfa" || !c.RequiresMFA {
		t.Fatalf("claims 不正确: %+v", c)
	}
	if c.Typ != TokenTypeMFA || c.IsAccessToken() {
		t.Fatalf("mfa 令牌用途必须是 mfa 且不能当 access：%+v", c)
	}
	if _, err := Parse(token, "wrong-secret"); err == nil {
		t.Fatal("错误 secret 不应通过")
	}
}

// 三种令牌的用途声明必须正确：只有 access 可以访问业务接口。
func TestTokenTypes(t *testing.T) {
	admin, err := IssueAdminTokenTTL("admin", "secret", true, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mfa, err := IssueMFAToken("admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	portal, err := IssuePortalTokenTTL("user@example.com", "secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		token string
		want  string
		ok    bool
	}{
		"admin":  {admin, TokenTypeAccess, true},
		"mfa":    {mfa, TokenTypeMFA, false},
		"portal": {portal, TokenTypeAccess, true},
	} {
		c, err := Parse(tc.token, "secret")
		if err != nil {
			t.Fatalf("%s 解析失败：%v", name, err)
		}
		if c.Typ != tc.want || c.IsAccessToken() != tc.ok {
			t.Fatalf("%s：typ=%q IsAccessToken=%v，期望 typ=%q ok=%v", name, c.Typ, c.IsAccessToken(), tc.want, tc.ok)
		}
	}
	// 旧版本没有 typ 字段的令牌按非 access 处理（部署后需要重新登录）。
	legacy, err := Sign(Claims{Sub: "admin", Exp: time.Now().Add(time.Hour).UnixMilli()}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if c, err := Parse(legacy, "secret"); err != nil || c.IsAccessToken() {
		t.Fatalf("旧版无 typ 令牌不应被视为 access：%+v err=%v", c, err)
	}
}
