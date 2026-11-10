// Package vpn 提供 MASQUE-VPN 集成的纯逻辑部分：管理通道选择、连接令牌、
// 授权判定。与数据库/HTTP 解耦，便于单测与在 server/agent 之间复用。
package vpn

import (
	"fmt"
	"strings"
	"time"
)

// Transport 是节点同步数据（管理通道）的传输方式。
//
// ProxyPilot 已有 WARP 组网；节点数据同步默认走 WARP，自建 MASQUE 管理网
// 作为备用，后期可在后台切换。
type Transport string

const (
	// TransportWarp 使用现有 Cloudflare WARP 组网地址（默认）。
	TransportWarp Transport = "warp"
	// TransportMasque 使用自建 MASQUE 管理网（星型，服务端为中心）。
	TransportMasque Transport = "masque"
	// TransportAuto 优先 WARP，WARP 不可用时回落自建管理网。
	TransportAuto Transport = "auto"
)

// ParseTransport 解析配置值，空值按 auto 处理。
func ParseTransport(s string) (Transport, error) {
	switch Transport(strings.ToLower(strings.TrimSpace(s))) {
	case "", TransportAuto:
		return TransportAuto, nil
	case TransportWarp:
		return TransportWarp, nil
	case TransportMasque:
		return TransportMasque, nil
	default:
		return "", fmt.Errorf("未知的节点管理通道 %q（warp|masque|auto）", s)
	}
}

// Settings 是 VPN 模块的全局设置（对应 vpn_settings 表）。
type Settings struct {
	Enabled             bool          `json:"enabled"`
	ManagementTransport Transport     `json:"management_transport"`
	MasqueFallback      bool          `json:"masque_fallback"`
	ServerMeshIPv4      string        `json:"server_mesh_ip4"`
	ServerMeshDomain    string        `json:"server_mesh_domain"`
	MeshCIDR4           string        `json:"mesh_cidr4"`
	ClientCIDR4         string        `json:"client_cidr4"`
	MTU                 int           `json:"mtu"`
	TokenTTL            time.Duration `json:"-"`
	TokenTTLSeconds     int           `json:"token_ttl_seconds"`
}

// DefaultSettings 返回与服务端迁移 0032 一致的默认值。
func DefaultSettings() Settings {
	return Settings{
		Enabled:             false,
		ManagementTransport: TransportWarp,
		MasqueFallback:      true,
		ServerMeshIPv4:      "10.99.0.1",
		ServerMeshDomain:    "vpncenter.yzfn.top",
		MeshCIDR4:           "10.99.0.0/16",
		ClientCIDR4:         "10.88.0.0/16",
		MTU:                 1380,
		TokenTTL:            10 * time.Minute,
		TokenTTLSeconds:     600,
	}
}

// Normalize 补齐零值并解析 Transport。
func (s *Settings) Normalize() error {
	def := DefaultSettings()
	if s.ManagementTransport == "" {
		s.ManagementTransport = def.ManagementTransport
	}
	if s.ServerMeshIPv4 == "" {
		s.ServerMeshIPv4 = def.ServerMeshIPv4
	}
	if s.ServerMeshDomain == "" {
		s.ServerMeshDomain = def.ServerMeshDomain
	}
	if s.MeshCIDR4 == "" {
		s.MeshCIDR4 = def.MeshCIDR4
	}
	if s.ClientCIDR4 == "" {
		s.ClientCIDR4 = def.ClientCIDR4
	}
	if s.MTU == 0 {
		s.MTU = def.MTU
	}
	if s.TokenTTLSeconds == 0 {
		s.TokenTTLSeconds = def.TokenTTLSeconds
	}
	if s.TokenTTL == 0 {
		s.TokenTTL = time.Duration(s.TokenTTLSeconds) * time.Second
	}
	tr, err := ParseTransport(string(s.ManagementTransport))
	if err != nil {
		return err
	}
	s.ManagementTransport = tr
	return s.Validate()
}

// Validate 检查取值范围。
func (s Settings) Validate() error {
	switch s.ManagementTransport {
	case TransportWarp, TransportMasque, TransportAuto:
	default:
		return fmt.Errorf("management_transport 非法: %q", s.ManagementTransport)
	}
	if s.MTU < 576 || s.MTU > 1500 {
		return fmt.Errorf("VPN MTU %d 超出 576-1500", s.MTU)
	}
	if s.TokenTTLSeconds < 60 || s.TokenTTLSeconds > 86400 {
		return fmt.Errorf("token_ttl_seconds %d 超出 60-86400", s.TokenTTLSeconds)
	}
	return nil
}

// EffectiveTransport 根据“WARP 是否可用”决定本次节点同步实际使用的通道。
//
//   - warp：始终走 WARP；
//   - masque：始终走自建管理网；
//   - auto：WARP 可用走 WARP，否则在允许回落时走自建管理网。
func (s Settings) EffectiveTransport(warpAvailable bool) Transport {
	switch s.ManagementTransport {
	case TransportWarp:
		return TransportWarp
	case TransportMasque:
		return TransportMasque
	default:
		if warpAvailable {
			return TransportWarp
		}
		if s.MasqueFallback {
			return TransportMasque
		}
		return TransportWarp
	}
}
