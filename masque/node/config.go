// Package node 实现 ProxyPilot 的 HTTP/3 IP 隧道节点守护进程：
// HTTP/3 接入（POST /tunnel 可靠流）、JWT 验签、TUN/NAT、用户隔离。
//
// 命名说明：包名/二进制名沿用历史命名 "masque"，但本实现并未采用 RFC 9484
// CONNECT-IP 与 RFC 9297 HTTP Datagram，因此不具备与标准 MASQUE 客户端互通的
// 能力；协议细节见 readme「VPN 与组网协议栈」。
package node

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Config 是节点守护进程的配置（JSON）。
type Config struct {
	Listen string `json:"listen"`
	// TLS 证书（HTTP/3 必须 TLS 1.3）。
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`

	NodeID     int64  `json:"node_id"`      // 与 JWT 的 node 声明比对
	MeshIPv4   string `json:"mesh_ip4"`     // 管理网地址（仅展示/心跳用）
	ClientCIDR string `json:"client_cidr4"` // 客户端子网，如 10.88.7.0/24
	Gateway4   string `json:"gateway_ip4"`  // TUN 地址，如 10.88.7.1

	TunName     string `json:"tun"`
	MTU         int    `json:"mtu"`
	MaxDatagram int    `json:"max_datagram"`
	WAN         string `json:"wan"` // 出口网卡，如 eth0；为空则自动探测默认路由

	// 连接令牌验签公钥（Ed25519，base64）。
	PublicKey string `json:"public_key"`
	Issuer    string `json:"issuer"`
	Audience  string `json:"audience"`

	DNS []string `json:"dns"`

	// Configure 为 true 时自动配置 TUN 地址/MTU、ip_forward 与 NAT/隔离规则。
	Configure bool `json:"configure"`

	// DisableNAT 为 true 时只配置 TUN 地址，不做 NAT/隔离（自建组网模式）。
	DisableNAT bool `json:"disable_nat"`

	// AllowClientToClient 允许客户端互访（组网模式）；默认只允许上网。
	AllowClientToClient bool `json:"allow_client_to_client"`

	// StatusSocket 可选：unix socket，输出 JSON 状态。
	StatusSocket string `json:"status_socket"`
}

// LoadConfig 读取并校验配置。
func LoadConfig(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, fmt.Errorf("解析配置: %w", err)
	}
	if err := c.Normalize(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Normalize 补齐默认值并做基本校验。
func (c *Config) Normalize() error {
	if strings.TrimSpace(c.Listen) == "" {
		c.Listen = "0.0.0.0:443"
	}
	if strings.TrimSpace(c.TunName) == "" {
		c.TunName = "ppvpn0"
	}
	if c.MTU <= 0 {
		c.MTU = 1280
	}
	if c.MaxDatagram <= 0 {
		c.MaxDatagram = 1360
	}
	if c.MaxDatagram < c.MTU {
		return fmt.Errorf("max_datagram(%d) 必须 >= mtu(%d)", c.MaxDatagram, c.MTU)
	}
	if strings.TrimSpace(c.Gateway4) == "" {
		return fmt.Errorf("gateway_ip4 必填")
	}
	if strings.TrimSpace(c.ClientCIDR) == "" {
		return fmt.Errorf("client_cidr4 必填")
	}
	if strings.TrimSpace(c.PublicKey) == "" {
		return fmt.Errorf("public_key 必填（Ed25519，base64）")
	}
	if _, err := base64.StdEncoding.DecodeString(strings.TrimSpace(c.PublicKey)); err != nil {
		return fmt.Errorf("public_key 不是合法 base64: %w", err)
	}
	if strings.TrimSpace(c.Issuer) == "" {
		c.Issuer = "proxypilot"
	}
	if strings.TrimSpace(c.Audience) == "" {
		c.Audience = "masque-node"
	}
	if c.CertFile == "" || c.KeyFile == "" {
		return fmt.Errorf("cert_file 与 key_file 必填")
	}
	return nil
}
