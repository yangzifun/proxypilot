package api

import (
	"net"
	"net/url"
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

// agentInstallCommand 生成节点安装命令。
//
// 关键约束：**脚本地址必须公网可达**。
// 之前这里用「Agent 访问地址」（内网 100.96.x.x）覆盖了 origin，结果生成的命令是
//   curl -fsSL http://100.96.0.18:80/api/agent/install.sh | sudo sh
// 新机器还没接入内网，这条命令根本拉不到脚本 —— 安装必然失败。
//
// 现在的分工：
//   脚本地址  = 分发基址（对象存储桶）> 面板公网地址；永远不用内网地址
//   --url     = Agent 的伺服地址，可以先用公网，Agent 上线后会自行切到内网通道
func (h *handlers) agentInstallCommand(ctx context.Context, origin, token string) string {
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")

	// --url 必须是「新机器刚装好就能连上」的地址。
	//
	// 组网地址（100.96.x.x）只有已入网的节点可达，而新节点此时还没组网，
	// 所以安装命令里绝不能出现内网地址。这里按优先级取：
	//   1) 站点公开地址（site_public_base_url，如 https://proxypilot.yangzifun.org）
	//   2) 请求来源（管理后台是用公网域名访问时即公网）
	//   3) Agent 访问地址（仅当它本身是公网地址时）
	// 节点装好后先走公网通道，组网完成后面板会下发内网同步地址，
	// Agent 探测到内网 /healthz 可用就自动切换过去（见 watchInternal）。
	if pub := h.publicBaseURL(ctx); pub != "" && !isPrivateHost(pub) {
		origin = pub
	} else if isPrivateHost(origin) {
		if v := strings.TrimRight(strings.TrimSpace(h.settingValue(ctx, settingAgentAccessURL)), "/"); v != "" && !isPrivateHost(v) {
			origin = v
		}
	}
	// 脚本基址 = 管理员设定的「Agent 分发基址」（agent_binary_base_url），
	// 安装脚本就发布在该路径下：<基址>/install.sh
	base := strings.TrimRight(strings.TrimSpace(firstNonEmptyStr(
		h.cfg.AgentInstallBase,
		h.settingValue(ctx, settingAgentBinaryBase),
	)), "/")
	return buildInstallCommand(base, origin, token)
}

// buildInstallCommand 是纯函数，便于单测：由「分发基址 + 伺服地址 + 密钥」拼命令。
//
// base 非空 → 走设定路径下的 install.sh（对象存储桶）；
// base 为空 → 回落到面板自身的 /api/agent/install.sh。
func buildInstallCommand(base, origin, token string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	origin = strings.TrimRight(strings.TrimSpace(origin), "/")
	if base != "" {
		return fmt.Sprintf("curl -fsSL %s/install.sh | sudo sh -s -- --key %s --url %s", base, token, origin)
	}
	return fmt.Sprintf("curl -fsSL %s/api/agent/install.sh | sudo sh -s -- --key %s --url %s", origin, token, origin)
}

// firstNonEmptyStr 返回第一个非空字符串。
func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

var errNoAgentKey = errors.New("未配置 AGENT_KEY_SECRET，无法加密接入令牌")

// isPrivateHost 判断 URL 是否指向内网/回环地址（这类地址不能用于对外安装命令）。
func isPrivateHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// 100.64.0.0/10 是运营商级 NAT 段（组网 mesh 就用它），Go 的 IsPrivate 不认，单独判断
	cgnat := &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip)
}