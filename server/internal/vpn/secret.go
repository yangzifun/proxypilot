package vpn

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const secretPrefix = "v1:"

func secretKey(secret string) [32]byte {
	return sha256.Sum256([]byte(secret))
}

// SealPrivateKey 用 AES-256-GCM 加密 Ed25519 私钥，返回 "v1:<base64(nonce|cipher)>"。
func SealPrivateKey(secret string, priv ed25519.PrivateKey) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", errors.New("vpn: 加密密钥不能为空")
	}
	key := secretKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := aead.Seal(nil, nonce, priv, nil)
	raw := append(nonce, ct...)
	return secretPrefix + base64.StdEncoding.EncodeToString(raw), nil
}

// OpenPrivateKey 解密 SealPrivateKey 的输出。
func OpenPrivateKey(secret, enc string) (ed25519.PrivateKey, error) {
	if !strings.HasPrefix(enc, secretPrefix) {
		return nil, fmt.Errorf("vpn: 不支持的密钥格式")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, secretPrefix))
	if err != nil {
		return nil, err
	}
	key := secretKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < aead.NonceSize() {
		return nil, errors.New("vpn: 密文过短")
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, errors.New("vpn: 私钥解密失败（密钥不匹配）")
	}
	if len(plain) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("vpn: 私钥长度 %d 非法", len(plain))
	}
	return ed25519.PrivateKey(plain), nil
}
