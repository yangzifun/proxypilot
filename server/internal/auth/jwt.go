package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// 令牌用途（Claims.Typ）。只有 access 令牌可以访问管理/门户接口；
// mfa 与 email_login 是一次性事务票据，只能用于各自的验证步骤。
const (
	// TokenTypeAccess 管理端 / 用户中心会话令牌（IssueAdminTokenTTL / IssuePortalTokenTTL）。
	TokenTypeAccess = "access"
	// TokenTypeMFA 密码通过后签发的 MFA 挑战票据（IssueMFAToken）。
	TokenTypeMFA = "mfa"
	// TokenTypeEmailLogin 邮箱验证码通过后签发的 MFA 事务票据。
	TokenTypeEmailLogin = "email_login"
)

// Claims 与 Worker 端签发的 JWT 载荷保持一致（exp 为 Unix 毫秒）。
type Claims struct {
	Sub           string `json:"sub"`
	Exp           int64  `json:"exp"`
	Role          string `json:"role,omitempty"`
	RequiresMFA   bool   `json:"requires_mfa,omitempty"`
	MFAVerified   bool   `json:"mfa_verified,omitempty"`
	ViaOIDC       bool   `json:"via_oidc,omitempty"`
	ViaBackupCode bool   `json:"via_backup_code,omitempty"`
	Purpose       string `json:"purpose,omitempty"`
	// Typ 明确令牌用途："access" | "mfa" | "email_login"。
	// 旧版本签发的令牌没有该字段，按"非 access"处理（部署后需重新登录）。
	Typ string `json:"typ,omitempty"`
}

// IsAccessToken 判断令牌是否为可访问业务接口的会话令牌。
// 只认显式的 access，缺省/其它用途一律拒绝（默认拒绝，避免再次出现用途混淆）。
func (c Claims) IsAccessToken() bool { return c.Typ == TokenTypeAccess }

// ErrInvalidToken 表示签名错误、结构错误或已过期。
var ErrInvalidToken = errors.New("token 无效或已过期")

type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// Sign 生成 HS256 JWT，格式与 Worker 完全一致。
func Sign(c Claims, secret string) (string, error) {
	head, err := json.Marshal(jwtHeader{Alg: "HS256", Typ: "JWT"})
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	return signingInput + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

// Parse 校验并解析 JWT。
func Parse(token, secret string) (Claims, error) {
	var c Claims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return c, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, want) {
		return c, ErrInvalidToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return c, ErrInvalidToken
	}
	if err := json.Unmarshal(payload, &c); err != nil {
		return c, ErrInvalidToken
	}
	if c.Sub == "" || c.Exp <= 0 {
		return c, ErrInvalidToken
	}
	if time.Now().UnixMilli() > c.Exp {
		return c, ErrInvalidToken
	}
	return c, nil
}

// IssueAdminToken 为后台账号签发 24 小时 token。
func IssueAdminToken(username, secret string, mfaVerified bool) (string, error) {
	return IssueAdminTokenTTL(username, secret, mfaVerified, 24*time.Hour)
}

// IssueAdminTokenTTL 按指定有效期签发管理端 token。
func IssueAdminTokenTTL(username, secret string, mfaVerified bool, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return Sign(Claims{
		Sub:         username,
		Exp:         time.Now().Add(ttl).UnixMilli(),
		RequiresMFA: mfaVerified,
		MFAVerified: mfaVerified,
		Typ:         TokenTypeAccess,
	}, secret)
}

// IssueMFAToken 签发 5 分钟的一次性 MFA 挑战 token（密码 + Turnstile 通过后使用）。
// 它不是登录 token：Purpose=mfa，只能用于 /api/login 的 verify_totp 步骤。
func IssueMFAToken(username, secret string) (string, error) {
	return Sign(Claims{
		Sub:         username,
		Exp:         time.Now().Add(5 * time.Minute).UnixMilli(),
		Purpose:     "mfa",
		RequiresMFA: true,
		Typ:         TokenTypeMFA,
	}, secret)
}

// IssuePortalToken 为用户中心签发 7 天 token（role=user）。
func IssuePortalToken(email, secret string) (string, error) {
	return IssuePortalTokenTTL(email, secret, 7*24*time.Hour)
}

// IssuePortalTokenTTL 按指定有效期签发用户中心 token。
func IssuePortalTokenTTL(email, secret string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	return Sign(Claims{
		Sub:  email,
		Exp:  time.Now().Add(ttl).UnixMilli(),
		Role: "user",
		Typ:  TokenTypeAccess,
	}, secret)
}
