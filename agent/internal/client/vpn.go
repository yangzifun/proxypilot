package client

import (
	"context"
	"fmt"
)

// VPNRegisterRequest 是节点向服务端注册 VPN 角色时的请求体。
//
// Warp 表示节点当前是否具备可用的 WARP 组网地址；服务端据此决定
// “节点同步数据”走 WARP（默认）还是回落到自建 MASQUE 管理网。
type VPNRegisterRequest struct {
	Name     string `json:"name,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Pubkey   string `json:"pubkey,omitempty"`
	Version  string `json:"version,omitempty"`
	Warp     bool   `json:"warp"`
}

// VPNRegisterResponse 是注册结果。
type VPNRegisterResponse struct {
	NodeID              int64  `json:"node_id"`
	State               string `json:"state"`
	MeshIPv4            string `json:"mesh_ip4"`
	ClientCIDR          string `json:"client_cidr4"`
	ManagementTransport string `json:"management_transport"`
	EffectiveTransport  string `json:"effective_transport"`
	MasqueFallback      bool   `json:"masque_fallback"`
	ServerMeshIPv4      string `json:"server_mesh_ip4"`
	MTU                 int    `json:"mtu"`
	TokenTTLSeconds     int    `json:"token_ttl_seconds"`
	MasqueEnabled       bool   `json:"masque_enabled"`
}

// VPNHeartbeatRequest 是节点链路心跳。
type VPNHeartbeatRequest struct {
	Version   string `json:"version,omitempty"`
	Transport string `json:"transport,omitempty"`
	State     string `json:"state,omitempty"`
	RTTms     int    `json:"rtt_ms,omitempty"`
}

// VPNRegister 注册节点 VPN 角色（可重复调用，幂等）。
func (c *Client) VPNRegister(ctx context.Context, req VPNRegisterRequest) (VPNRegisterResponse, error) {
	var out VPNRegisterResponse
	if _, err := c.post(ctx, "/api/agent/vpn/register", req, &out); err != nil {
		return out, fmt.Errorf("VPN 节点注册失败: %w", err)
	}
	return out, nil
}

// VPNHeartbeat 上报节点链路状态。
func (c *Client) VPNHeartbeat(ctx context.Context, req VPNHeartbeatRequest) error {
	if _, err := c.post(ctx, "/api/agent/vpn/heartbeat", req, nil); err != nil {
		return fmt.Errorf("VPN 节点心跳失败: %w", err)
	}
	return nil
}
