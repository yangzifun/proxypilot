package api

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"
	"time"
)

func signRS256Test(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	input := enc.EncodeToString(head) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + enc.EncodeToString(sig)
}

func TestVerifyRS256JWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	jwks := jwksDoc{}
	jwks.Keys = append(jwks.Keys, struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
		Alg string `json:"alg"`
		Use string `json:"use"`
	}{
		Kty: "RSA", Kid: "k1",
		N:   enc.EncodeToString(key.PublicKey.N.Bytes()),
		E:   enc.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
		Alg: "RS256", Use: "sig",
	})

	token := signRS256Test(t, key, map[string]any{
		"iss": "https://id.example.com", "aud": "client", "sub": "u1",
		"exp":   float64(time.Now().Add(time.Hour).Unix()),
		"nonce": "n1", "email": "a@example.com", "email_verified": true,
	})
	claims, err := verifyRS256JWT(token, jwks, "https://id.example.com", "client", "n1")
	if err != nil {
		t.Fatalf("验签失败: %v", err)
	}
	if jsonStr(claims["email"]) != "a@example.com" {
		t.Fatalf("claims = %#v", claims)
	}

	// nonce 不匹配
	if _, err := verifyRS256JWT(token, jwks, "https://id.example.com", "client", "bad"); err == nil {
		t.Fatal("nonce 不匹配应失败")
	}
	// aud 不匹配
	if _, err := verifyRS256JWT(token, jwks, "https://id.example.com", "other", "n1"); err == nil {
		t.Fatal("aud 不匹配应失败")
	}
	// 篡改 payload
	parts := token[:len(token)-4] + "AAAA"
	if _, err := verifyRS256JWT(parts, jwks, "https://id.example.com", "client", "n1"); err == nil {
		t.Fatal("签名被篡改应失败")
	}
}

func TestPKCEChallenge(t *testing.T) {
	v := randomVerifier()
	c := pkceChallenge(v)
	if len(v) != 43 || len(c) != 43 {
		t.Fatalf("verifier/challenge 长度 = %d/%d", len(v), len(c))
	}
	if c == v {
		t.Fatal("challenge 不应等于 verifier")
	}
	if len(randomState()) != 64 {
		t.Fatal("state 应为 64 位 hex")
	}
}
