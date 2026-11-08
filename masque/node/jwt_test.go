package node

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func signToken(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": "k1"})
	p, _ := json.Marshal(c)
	input := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(p)
	sig := ed25519.Sign(priv, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestVerifier(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(base64.StdEncoding.EncodeToString(pub), "proxypilot", "masque-node")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	v.SetClock(func() time.Time { return now })

	c := Claims{
		Issuer: "proxypilot", Audience: "masque-node", Subject: "device:1",
		NodeID: 7, GrantID: 3, IPv4: "10.88.7.23", IssuedAt: now.Unix(),
		ExpiresAt: now.Add(10 * time.Minute).Unix(), JTI: "j1",
	}
	token := signToken(t, priv, c)
	got, err := v.Verify(token)
	if err != nil {
		t.Fatal(err)
	}
	if got.IPv4 != "10.88.7.23" || got.NodeID != 7 {
		t.Fatalf("claims = %+v", got)
	}

	expired := c
	expired.ExpiresAt = now.Add(-time.Minute).Unix()
	if _, err := v.Verify(signToken(t, priv, expired)); err == nil {
		t.Fatal("过期令牌未被拒绝")
	}

	wrongAud := c
	wrongAud.Audience = "other"
	if _, err := v.Verify(signToken(t, priv, wrongAud)); err == nil {
		t.Fatal("错误 audience 未被拒绝")
	}

	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	_ = otherPub
	if _, err := v.Verify(signToken(t, otherPriv, c)); err == nil {
		t.Fatal("错误签名未被拒绝")
	}
}
