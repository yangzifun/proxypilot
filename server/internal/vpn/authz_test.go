package vpn

import (
	"errors"
	"testing"
	"time"
)

func baseInput(now time.Time) AuthInput {
	return AuthInput{
		Now:             now,
		UserUUID:        "u1",
		Access:          Access{Enabled: true, MaxDevices: 3},
		Node:            Node{ID: 7, State: "active", Endpoint: "hk1.example.com:443"},
		Grant:           &Grant{ID: 9, NodeID: 7, SubjectID: "u1", Enabled: true, MaxDevices: 2},
		Device:          Device{ID: 5, UUID: "u1", State: "active"},
		UserDeviceCount: 1,
		NodeDeviceCount: 0,
	}
}

func codeOf(err error) ErrorCode {
	var d *DeniedError
	if errors.As(err, &d) {
		return d.Code
	}
	return ""
}

func TestAuthorizeAllow(t *testing.T) {
	if err := Authorize(baseInput(time.Now())); err != nil {
		t.Fatalf("应允许: %v", err)
	}
}

func TestAuthorizeDenied(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	cases := []struct {
		name string
		mod  func(*AuthInput)
		want ErrorCode
	}{
		{"未开通", func(in *AuthInput) { in.Access.Enabled = false }, CodeUserNotAuthorized},
		{"资格过期", func(in *AuthInput) { in.Access.ExpiresAt = past }, CodeUserNotAuthorized},
		{"用户配额", func(in *AuthInput) { in.Access.QuotaBytes = 100; in.Access.UsedBytes = 100 }, CodeUserQuotaExceeded},
		{"节点停用", func(in *AuthInput) { in.Node.State = "disabled" }, CodeNodeOffline},
		{"无绑定", func(in *AuthInput) { in.Grant = nil }, CodeNodeNotBound},
		{"绑定停用", func(in *AuthInput) { in.Grant.Enabled = false }, CodeNodeNotBound},
		{"绑定节点不符", func(in *AuthInput) { in.Grant.NodeID = 8 }, CodeNodeNotBound},
		{"绑定用户不符", func(in *AuthInput) { in.Grant.SubjectID = "u2" }, CodeNodeNotBound},
		{"绑定过期", func(in *AuthInput) { in.Grant.ExpiresAt = past }, CodeGrantExpired},
		{"绑定未到期", func(in *AuthInput) { in.Grant.ExpiresAt = future }, ""},
		{"节点配额", func(in *AuthInput) { in.Grant.QuotaBytes = 10; in.Grant.UsedBytes = 10 }, CodeNodeQuotaExceeded},
		{"设备吊销", func(in *AuthInput) { in.Device.State = "revoked" }, CodeDeviceRevoked},
		{"设备不属于用户", func(in *AuthInput) { in.Device.UUID = "u2" }, CodeDeviceNotOwned},
		{"用户设备超限", func(in *AuthInput) { in.UserDeviceCount = 4 }, CodeDeviceLimit},
		{"节点并发超限", func(in *AuthInput) { in.NodeDeviceCount = 2 }, CodeDeviceLimit},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseInput(now)
			tc.mod(&in)
			err := Authorize(in)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("应允许，得到 %v", err)
				}
				return
			}
			if got := codeOf(err); got != tc.want {
				t.Fatalf("code = %q，期望 %q（err=%v）", got, tc.want, err)
			}
		})
	}
}

func TestVisible(t *testing.T) {
	now := time.Now()
	in := baseInput(now)
	if !Visible(in.Access, in.Node, in.Grant, now) {
		t.Fatal("应可见")
	}
	if Visible(in.Access, in.Node, nil, now) {
		t.Fatal("无绑定不应可见")
	}
	expiredAccess := in.Access
	expiredAccess.ExpiresAt = now.Add(-time.Minute)
	if Visible(expiredAccess, in.Node, in.Grant, now) {
		t.Fatal("资格过期不应可见")
	}
	offline := in.Node
	offline.State = "disabled"
	if Visible(in.Access, offline, in.Grant, now) {
		t.Fatal("节点停用不应可见")
	}
}

