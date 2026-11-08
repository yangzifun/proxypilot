package vpn

import (
	"fmt"
	"time"
)

// ErrorCode 是连接被拒绝的原因码；前端按码展示文案，不暴露内部细节。
type ErrorCode string

const (
	CodeUserNotAuthorized ErrorCode = "user_not_authorized"
	CodeUserQuotaExceeded ErrorCode = "user_quota_exceeded"
	CodeNodeNotBound      ErrorCode = "node_not_bound"
	CodeGrantExpired      ErrorCode = "grant_expired"
	CodeNodeQuotaExceeded ErrorCode = "node_quota_exceeded"
	CodeDeviceLimit       ErrorCode = "device_limit"
	CodeDeviceRevoked     ErrorCode = "device_revoked"
	CodeDeviceNotOwned    ErrorCode = "device_not_owned"
	CodeNodeOffline       ErrorCode = "node_offline"
)

// DeniedError 带原因码的拒绝错误。
type DeniedError struct {
	Code    ErrorCode
	Message string
}

func (e *DeniedError) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Deny 构造拒绝错误。
func Deny(code ErrorCode, format string, args ...any) *DeniedError {
	return &DeniedError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Access 是用户级 VPN 资格（vpn_user_access）。
type Access struct {
	Enabled    bool
	ExpiresAt  time.Time
	MaxDevices int
	QuotaBytes int64
	UsedBytes  int64
}

// Grant 是节点绑定（vpn_node_grants）。
type Grant struct {
	ID         int64
	NodeID     int64
	SubjectID  string
	Enabled    bool
	ExpiresAt  time.Time
	MaxDevices int
	QuotaBytes int64
	UsedBytes  int64
}

// Device 是用户设备（vpn_devices）。
type Device struct {
	ID    int64
	UUID  string
	State string
}

// Node 是 VPN 节点（vpn_nodes）。
type Node struct {
	ID       int64
	State    string
	Endpoint string
}

// AuthInput 是一次连接判定的全部输入。
type AuthInput struct {
	Now             time.Time
	UserUUID        string
	Access          Access
	Node            Node
	Grant           *Grant
	Device          Device
	UserDeviceCount int
	NodeDeviceCount int
}

func expired(t time.Time, now time.Time) bool {
	return !t.IsZero() && !now.Before(t)
}

func quotaExceeded(quota, used int64) bool {
	return quota > 0 && used >= quota
}

// Visible 判断节点是否应出现在该用户的节点列表中：
// 用户资格有效 + 节点在线 + 绑定有效（不检查设备与并发）。
func Visible(access Access, node Node, grant *Grant, now time.Time) bool {
	if !access.Enabled || expired(access.ExpiresAt, now) || quotaExceeded(access.QuotaBytes, access.UsedBytes) {
		return false
	}
	if node.State != "active" || node.Endpoint == "" {
		return false
	}
	if grant == nil || !grant.Enabled || expired(grant.ExpiresAt, now) {
		return false
	}
	if grant.NodeID != 0 && node.ID != 0 && grant.NodeID != node.ID {
		return false
	}
	return !quotaExceeded(grant.QuotaBytes, grant.UsedBytes)
}

// Authorize 执行连接前的完整授权判定；返回 nil 表示允许。
//
// 判定顺序（任一失败即拒绝）：
//  1. 用户资格：开关、到期、总配额；
//  2. 节点：active 且配置了接入地址；
//  3. 绑定：存在、启用、未到期、节点匹配、节点配额；
//  4. 设备：属于该用户、未吊销；
//  5. 并发：用户设备总数、该节点上的设备数。
func Authorize(in AuthInput) error {
	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	// 1. 用户资格
	if !in.Access.Enabled {
		return Deny(CodeUserNotAuthorized, "用户未开通 VPN")
	}
	if expired(in.Access.ExpiresAt, now) {
		return Deny(CodeUserNotAuthorized, "VPN 资格已到期")
	}
	if quotaExceeded(in.Access.QuotaBytes, in.Access.UsedBytes) {
		return Deny(CodeUserQuotaExceeded, "用户总流量配额已用完")
	}

	// 2. 节点状态
	if in.Node.State != "active" || in.Node.Endpoint == "" {
		return Deny(CodeNodeOffline, "节点未启用或没有接入地址")
	}

	// 3. 绑定
	g := in.Grant
	if g == nil || !g.Enabled {
		return Deny(CodeNodeNotBound, "该节点未授权给此用户")
	}
	if g.NodeID != 0 && in.Node.ID != 0 && g.NodeID != in.Node.ID {
		return Deny(CodeNodeNotBound, "绑定与节点不匹配")
	}
	if g.SubjectID != "" && in.UserUUID != "" && g.SubjectID != in.UserUUID {
		return Deny(CodeNodeNotBound, "绑定与用户不匹配")
	}
	if expired(g.ExpiresAt, now) {
		return Deny(CodeGrantExpired, "节点授权已到期")
	}
	if quotaExceeded(g.QuotaBytes, g.UsedBytes) {
		return Deny(CodeNodeQuotaExceeded, "该节点流量配额已用完")
	}

	// 4. 设备
	if in.Device.State != "active" {
		return Deny(CodeDeviceRevoked, "设备已吊销")
	}
	if in.Device.UUID != "" && in.UserUUID != "" && in.Device.UUID != in.UserUUID {
		return Deny(CodeDeviceNotOwned, "设备不属于该用户")
	}

	// 5. 并发
	if in.Access.MaxDevices > 0 && in.UserDeviceCount > in.Access.MaxDevices {
		return Deny(CodeDeviceLimit, "设备数量超过用户上限（%d）", in.Access.MaxDevices)
	}
	if g.MaxDevices > 0 && in.NodeDeviceCount >= g.MaxDevices {
		return Deny(CodeDeviceLimit, "该节点并发设备数超过上限（%d）", g.MaxDevices)
	}
	return nil
}
