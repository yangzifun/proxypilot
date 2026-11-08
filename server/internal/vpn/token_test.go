package vpn

import (
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"
)

func testKeyFunc(pub ed25519.PublicKey) KeyFunc {
	return func(kid string) (ed25519.PublicKey, error) {
		if kid == "" {
			return nil, ErrUnknownKey
		}
		return pub, nil
	}
}

func TestTokenIssueVerify(t *testing.T) {
	s, err := GenerateSigner()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })

	token, err := s.Issue(Claims{
		Subject: "device:7",
		UUID:    "user-uuid",
		NodeID:  3,
		GrantID: 9,
		IPv4:    "10.88.7.23",
		Network: "default",
	}, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	got, err := Verify(token, testKeyFunc(s.PublicKey()), now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "device:7" || got.NodeID != 3 || got.GrantID != 9 {
		t.Fatalf("claims mismatch: %+v", got)
	}
	if got.Issuer != TokenIssuer || got.Audience != TokenAudience {
		t.Fatalf("iss/aud mismatch: %+v", got)
	}
	if got.ExpiresAt != now.Unix()+600 {
		t.Fatalf("exp = %d", got.ExpiresAt)
	}
	if got.JTI == "" {
		t.Fatal("missing jti")
	}
}

func TestTokenExpired(t *testing.T) {
	s, _ := GenerateSigner()
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })

	token, _ := s.Issue(Claims{Subject: "device:1", NodeID: 1}, time.Minute)
	if _, err := Verify(token, testKeyFunc(s.PublicKey()), now.Add(2*time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v，期望 ErrTokenExpired", err)
	}
}

func TestTokenTampered(t *testing.T) {
	s, _ := GenerateSigner()
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	token, _ := s.Issue(Claims{Subject: "device:1", NodeID: 1}, time.Minute)

	parts := strings.Split(token, ".")
	parts[1] = parts[1][:len(parts[1])-2] + "AA"
	if _, err := Verify(strings.Join(parts, "."), testKeyFunc(s.PublicKey()), now); err == nil {
		t.Fatal("篡改后的令牌没有被拒绝")
	}
}

func TestTokenUnknownKey(t *testing.T) {
	s, _ := GenerateSigner()
	token, _ := s.Issue(Claims{Subject: "device:1", NodeID: 1}, time.Minute)
	if _, err := Verify(token, func(string) (ed25519.PublicKey, error) {
		return nil, ErrUnknownKey
	}, time.Now()); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("err = %v，期望 ErrUnknownKey", err)
	}
}

func TestTokenWrongSignature(t *testing.T) {
	s1, _ := GenerateSigner()
	s2, _ := GenerateSigner()
	now := time.Unix(1_800_000_000, 0)
	s1.SetClock(func() time.Time { return now })

	token, _ := s1.Issue(Claims{Subject: "device:1", NodeID: 1}, time.Minute)
	// 用 s2 的公钥验 s1 的签名
	if _, err := Verify(token, testKeyFunc(s2.PublicKey()), now); !errors.Is(err, ErrTokenSignature) {
		t.Fatalf("err = %v，期望 ErrTokenSignature", err)
	}
}

func TestTokenRequiresFields(t *testing.T) {
	s, _ := GenerateSigner()
	if _, err := s.Issue(Claims{NodeID: 1}, time.Minute); err == nil {
		t.Fatal("缺少 subject 应失败")
	}
	if _, err := s.Issue(Claims{Subject: "device:1"}, time.Minute); err == nil {
		t.Fatal("缺少 node 应失败")
	}
}

func TestPublicKeyBase64(t *testing.T) {
	s, _ := GenerateSigner()
	enc := PublicKeyBase64(s.PublicKey())
	pub, err := ParsePublicKeyBase64(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !pub.Equal(s.PublicKey()) {
		t.Fatal("公钥往返不一致")
	}
}
