package vpn

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// TokenIssuer 是连接令牌的签发者。
	TokenIssuer = "proxypilot"
	// TokenAudience 是连接令牌的受众（节点侧校验）。
	TokenAudience = "masque-node"
	// tokenClockSkew 允许的时钟偏差。
	tokenClockSkew = 30 * time.Second
)

var (
	// ErrTokenMalformed 表示令牌结构不合法。
	ErrTokenMalformed = errors.New("vpn: 令牌格式错误")
	// ErrTokenSignature 表示签名校验失败。
	ErrTokenSignature = errors.New("vpn: 令牌签名无效")
	// ErrTokenExpired 表示令牌已过期。
	ErrTokenExpired = errors.New("vpn: 令牌已过期")
	// ErrTokenAudience 表示受众不匹配。
	ErrTokenAudience = errors.New("vpn: 令牌受众不匹配")
	// ErrTokenIssuer 表示签发者不匹配。
	ErrTokenIssuer = errors.New("vpn: 令牌签发者不匹配")
	// ErrUnknownKey 表示 kid 没有对应的公钥。
	ErrUnknownKey = errors.New("vpn: 未知的签名密钥")
)

// Claims 是客户端 → 节点的连接令牌内容。
//
// 节点离线验签；令牌短 TTL（默认 10 分钟），吊销依赖短 TTL + 节点侧吊销列表。
type Claims struct {
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	Subject   string `json:"sub"` // device:<id>
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

// KeyFunc 按 kid 返回验签公钥。
type KeyFunc func(kid string) (ed25519.PublicKey, error)

// Signer 持有 Ed25519 签名密钥。
type Signer struct {
	kid  string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	now  func() time.Time
}

// NewSigner 用已有私钥构造签名器。
func NewSigner(kid string, priv ed25519.PrivateKey) (*Signer, error) {
	if strings.TrimSpace(kid) == "" {
		return nil, errors.New("vpn: kid 不能为空")
	}
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("vpn: 私钥长度 %d 非法", len(priv))
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("vpn: 私钥类型非法")
	}
	return &Signer{kid: kid, priv: priv, pub: pub, now: time.Now}, nil
}

// GenerateSigner 生成新的密钥对与 kid。
func GenerateSigner() (*Signer, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	return &Signer{
		kid:  "k" + hex.EncodeToString(raw[:]),
		priv: priv,
		pub:  pub,
		now:  time.Now,
	}, nil
}

// Kid 返回密钥 ID。
func (s *Signer) Kid() string { return s.kid }

// PublicKey 返回公钥副本。
func (s *Signer) PublicKey() ed25519.PublicKey {
	out := make(ed25519.PublicKey, len(s.pub))
	copy(out, s.pub)
	return out
}

// PrivateKey 返回私钥副本（仅用于加密持久化）。
func (s *Signer) PrivateKey() ed25519.PrivateKey {
	out := make(ed25519.PrivateKey, len(s.priv))
	copy(out, s.priv)
	return out
}

// SetClock 仅供测试注入时间。
func (s *Signer) SetClock(fn func() time.Time) {
	if fn != nil {
		s.now = fn
	}
}

// Issue 签发连接令牌。ttl<=0 时使用 10 分钟。
func (s *Signer) Issue(c Claims, ttl time.Duration) (string, error) {
	if strings.TrimSpace(c.Subject) == "" {
		return "", errors.New("vpn: 令牌缺少 subject")
	}
	if c.NodeID <= 0 {
		return "", errors.New("vpn: 令牌缺少 node")
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	now := s.now().Unix()
	c.Issuer = TokenIssuer
	c.Audience = TokenAudience
	c.IssuedAt = now
	c.ExpiresAt = now + int64(ttl.Seconds())
	if c.JTI == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		c.JTI = base64.RawURLEncoding.EncodeToString(raw[:])
	}

	header, err := json.Marshal(tokenHeader{Alg: "EdDSA", Typ: "JWT", Kid: s.kid})
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	signingInput := b64(header) + "." + b64(payload)
	sig := ed25519.Sign(s.priv, []byte(signingInput))
	return signingInput + "." + b64(sig), nil
}

// Verify 校验令牌并返回 Claims。
func Verify(token string, keys KeyFunc, now time.Time) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrTokenMalformed
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var h tokenHeader
	if err := json.Unmarshal(headerJSON, &h); err != nil {
		return nil, ErrTokenMalformed
	}
	if h.Alg != "EdDSA" || h.Kid == "" {
		return nil, ErrTokenMalformed
	}
	pub, err := keys(h.Kid)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, ErrUnknownKey
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, ErrTokenMalformed
	}
	if !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, ErrTokenSignature
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var c Claims
	if err := json.Unmarshal(payloadJSON, &c); err != nil {
		return nil, ErrTokenMalformed
	}
	if c.Issuer != TokenIssuer {
		return nil, ErrTokenIssuer
	}
	if c.Audience != TokenAudience {
		return nil, ErrTokenAudience
	}
	if c.ExpiresAt <= 0 || now.Unix() > c.ExpiresAt+int64(tokenClockSkew.Seconds()) {
		return nil, ErrTokenExpired
	}
	if c.Subject == "" || c.NodeID <= 0 || c.JTI == "" {
		return nil, ErrTokenMalformed
	}
	return &c, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// PublicKeyBase64 便于把公钥写入 vpn_signing_keys。
func PublicKeyBase64(pub ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub)
}

// ParsePublicKeyBase64 解析存储的公钥。
func ParsePublicKeyBase64(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("vpn: 公钥长度 %d 非法", len(raw))
	}
	return ed25519.PublicKey(raw), nil
}


