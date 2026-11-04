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
}

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
	}, secret)
}
