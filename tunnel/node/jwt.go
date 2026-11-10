package node

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const tokenClockSkew = 30 * time.Second

// Claims 与服务端签发的连接令牌一致。
type Claims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"`
	UUID      string `json:"uuid,omitempty"`
	NodeID    int64  `json:"node,omitempty"`
	GrantID   int64  `json:"grant,omitempty"`
	IPv4      string `json:"ip4,omitempty"`
	IPv6      string `json:"ip6,omitempty"`
	Network   string `json:"net,omitempty"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	JTI       string `json:"jti"`
}

type tokenHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid"`
}

// ErrInvalidToken 表示令牌不可用。
var ErrInvalidToken = errors.New("节点：无效的连接令牌")

// Verifier 校验 Ed25519 连接令牌。
type Verifier struct {
	pub      ed25519.PublicKey
	issuer   string
	audience string
	now      func() time.Time
}

// NewVerifier 用 base64 公钥构造验签器。
// legacyAudience 是 36.0 之前的 audience 名。
//
// 改名（masque-node -> pp-tunnel）必须能平滑：节点同时接受新旧两个值，
// 这样旧客户端不会因为节点升级而全部鉴权失败。等所有客户端都升到 3.0
// 之后再把这个兼容项删掉。
const (
	// currentAudience 是 36.0 起的正式名称。
	currentAudience = "pp-tunnel"
	// legacyAudience 是 36.0 之前的名称，过渡期仍接受。
	legacyAudience = "masque-node"
)

// audienceAccepted 判断 audience 是否被接受。
//
// 只允许 {新名, 旧名} 这一对互相兼容，其他值一律拒绝 ——
// 如果写成「等于任一旧名就放行」，那么当节点还配着旧名时，
// 任何 audience 都会被接受，等于关掉了校验。
func audienceAccepted(got, want string) bool {
	if got == want {
		return true
	}
	pair := func(a, b string) bool {
		return (a == currentAudience && b == legacyAudience) ||
			(a == legacyAudience && b == currentAudience)
	}
	return pair(got, want)
}

func NewVerifier(pubB64, issuer, audience string) (*Verifier, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil {
		return nil, fmt.Errorf("公钥 base64 解析失败: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("公钥长度 %d 非法", len(raw))
	}
	if issuer == "" || audience == "" {
		return nil, errors.New("issuer/audience 不能为空")
	}
	return &Verifier{pub: ed25519.PublicKey(raw), issuer: issuer, audience: audience, now: time.Now}, nil
}

// SetClock 仅供测试。
func (v *Verifier) SetClock(fn func() time.Time) {
	if fn != nil {
		v.now = fn
	}
}

// Verify 校验令牌并返回 Claims。
func (v *Verifier) Verify(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidToken
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var h tokenHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil || h.Alg != "EdDSA" {
		return nil, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrInvalidToken
	}
	if !ed25519.Verify(v.pub, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, ErrInvalidToken
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidToken
	}
	var c Claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return nil, ErrInvalidToken
	}
	if c.Issuer != v.issuer || !audienceAccepted(c.Audience, v.audience) {
		return nil, ErrInvalidToken
	}
	if c.ExpiresAt <= 0 || v.now().Unix() > c.ExpiresAt+int64(tokenClockSkew.Seconds()) {
		return nil, ErrInvalidToken
	}
	if c.Subject == "" || c.NodeID <= 0 || c.IPv4 == "" || c.JTI == "" {
		return nil, ErrInvalidToken
	}
	return &c, nil
}
