package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	agentTokenPrefix = "ag_"
	agentTokenBytes  = 32
	agentKeySalt     = "proxypilot-agent-key-v1"
	agentKeyInfo     = "agent-token-v1"
	agentKeyVersion  = "enc:v1:"
)

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// generateAgentToken 生成 ag_<64 位 hex>，与 Worker 的 AGENT_TOKEN_BYTES=32 一致。
func generateAgentToken() (string, error) {
	buf := make([]byte, agentTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return agentTokenPrefix + hex.EncodeToString(buf), nil
}

// agentTokenHint 复刻 Worker：ag_ + 4 位 + **** + 后 4 位。
func agentTokenHint(token string) string {
	s := strings.TrimSpace(token)
	if len(s) <= len(agentTokenPrefix)+8 {
		return s[:minInt(len(s), len(agentTokenPrefix)+2)] + "****"
	}
	return s[:len(agentTokenPrefix)+4] + "****" + s[len(s)-4:]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// hkdfSHA256 实现 RFC 5869 HKDF-SHA256（仅用标准库）。
func hkdfSHA256(secret, salt, info []byte, length int) []byte {
	extract := hmac.New(sha256.New, salt)
	extract.Write(secret)
	prk := extract.Sum(nil)

	var out []byte
	var prev []byte
	for counter := byte(1); len(out) < length; counter++ {
		mac := hmac.New(sha256.New, prk)
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{counter})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:length]
}

func (h *handlers) agentKeySecret() string {
	if s := strings.TrimSpace(h.cfg.AgentKeySecret); s != "" {
		return s
	}
	return h.cfg.JWTSecret
}

// encryptAgentToken 输出 enc:v1:<base64(iv||ciphertext)>，与 Worker 格式一致。
func encryptAgentToken(secret, token string) (string, error) {
	key := hkdfSHA256([]byte(secret), []byte(agentKeySalt), []byte(agentKeyInfo), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(iv, iv, []byte(token), nil)
	return agentKeyVersion + base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptAgentToken 解密失败时返回空串（历史数据 / 密钥轮换 / 密文损坏）。
func decryptAgentToken(secret, enc string) string {
	raw := strings.TrimPrefix(strings.TrimSpace(enc), agentKeyVersion)
	if raw == "" || !strings.HasPrefix(strings.TrimSpace(enc), agentKeyVersion) {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(blob) < 13 {
		return ""
	}
	key := hkdfSHA256([]byte(secret), []byte(agentKeySalt), []byte(agentKeyInfo), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(blob) < gcm.NonceSize() {
		return ""
	}
	plain, err := gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// agentInstallCommand 复刻 Worker：优先对象存储脚本，否则用本站 /api/agent/install.sh。
func (h *handlers) agentInstallCommand(ctx context.Context, origin, token string) string {
	// 节点直连地址优先（系统管理 → Agent 访问地址），留空回落到请求来源
	if v := strings.TrimSpace(h.settingValue(ctx, settingAgentAccessURL)); v != "" {
		origin = v
	}
	base := strings.TrimRight(strings.TrimSpace(h.cfg.AgentInstallBase), "/")
	if base != "" {
		return fmt.Sprintf("curl -fsSL %s/install.sh | sudo sh -s -- --key %s --url %s", base, token, origin)
	}
	return fmt.Sprintf("curl -fsSL %s/api/agent/install.sh | sudo sh -s -- --key %s", strings.TrimRight(origin, "/"), token)
}

var errNoAgentKey = errors.New("未配置 AGENT_KEY_SECRET，无法加密接入令牌")
