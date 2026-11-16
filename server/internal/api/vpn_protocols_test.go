package api

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

func TestParseVPNProtocol(t *testing.T) {
	cases := []struct {
		in    string
		want  string
		isErr bool
	}{
		{"", vpnProtocolPrivate, false},
		{"private", vpnProtocolPrivate, false},
		{"PRIVATE", vpnProtocolPrivate, false},
		{" private ", vpnProtocolPrivate, false},
		{"masque", vpnProtocolMasque, false},
		{"MASQUE", vpnProtocolMasque, false},
		{" masque ", vpnProtocolMasque, false},
		{"wireguard", "", true},
		{"vless", "", true},
		{"private,masque", "", true},
	}
	for _, c := range cases {
		got, err := parseVPNProtocol(c.in)
		if c.isErr {
			if err == nil {
				t.Fatalf("parseVPNProtocol(%q) 期望报错，实际返回 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseVPNProtocol(%q) 意外报错：%v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("parseVPNProtocol(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func protocolErrCode(t *testing.T, err error) string {
	t.Helper()
	var pe *vpnProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("期望 *vpnProtocolError，实际 %T: %v", err, err)
	}
	return pe.Code
}

func TestResolveConnectProtocol(t *testing.T) {
	privateOnly := vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: false}

	// 1) 旧节点（仅私有协议）：无 protocol / private 均可连接
	for _, req := range []string{"", "private"} {
		got, err := resolveConnectProtocol(privateOnly, req)
		if err != nil || got != vpnProtocolPrivate {
			t.Fatalf("private 节点请求 %q：got=%q err=%v", req, got, err)
		}
	}
	// 2) 旧节点请求 MASQUE：明确拒绝（未启用），不能假成功
	if _, err := resolveConnectProtocol(privateOnly, "masque"); err == nil {
		t.Fatal("未启用 MASQUE 的节点不应允许 masque 连接")
	} else if code := protocolErrCode(t, err); code != "protocol_not_enabled" {
		t.Fatalf("错误码 = %q，期望 protocol_not_enabled", code)
	}

	// 3) 已启用但未就绪（pending）：拒绝并给出就绪状态
	pending := vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true, MasqueState: masqueStatePending}
	if _, err := resolveConnectProtocol(pending, "masque"); err == nil {
		t.Fatal("MASQUE pending 状态不应允许连接")
	} else if code := protocolErrCode(t, err); code != "protocol_not_ready" {
		t.Fatalf("错误码 = %q，期望 protocol_not_ready", code)
	} else if !strings.Contains(err.Error(), masqueStatePending) {
		t.Fatalf("错误文案应包含当前状态 %s：%v", masqueStatePending, err)
	}

	// 4) 已就绪：允许 MASQUE
	ready := vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true, MasqueState: masqueStateReady}
	if got, err := resolveConnectProtocol(ready, "masque"); err != nil || got != vpnProtocolMasque {
		t.Fatalf("ready 状态应允许 masque：got=%q err=%v", got, err)
	}

	// 5) 私有协议被关闭：默认请求应被拒绝（节点只对 MASQUE 开放）
	masqueOnly := vpnNodeProtocols{PrivateEnabled: false, MasqueEnabled: true, MasqueState: masqueStateReady}
	if _, err := resolveConnectProtocol(masqueOnly, ""); err == nil {
		t.Fatal("私有协议关闭时默认（private）应被拒绝")
	} else if code := protocolErrCode(t, err); code != "protocol_not_enabled" {
		t.Fatalf("错误码 = %q，期望 protocol_not_enabled", code)
	}

	// 6) 非法协议：400
	if _, err := resolveConnectProtocol(privateOnly, "wireguard"); err == nil {
		t.Fatal("非法协议应报错")
	} else {
		var pe *vpnProtocolError
		if !errors.As(err, &pe) || pe.Status != 400 || pe.Code != "protocol_invalid" {
			t.Fatalf("非法协议错误 = %+v，期望 400/protocol_invalid", pe)
		}
	}

	// 7) 两者都关闭的异常数据：不应允许任何协议
	bothOff := vpnNodeProtocols{PrivateEnabled: false, MasqueEnabled: false}
	if _, err := resolveConnectProtocol(bothOff, ""); err == nil {
		t.Fatal("两种协议都关闭时应拒绝连接")
	}
}

func TestNodeProtocolsNormalized(t *testing.T) {
	// 全零值：路径/鉴权补默认，状态 disabled
	p := vpnNodeProtocols{}.normalized()
	if p.MasquePath != defaultMasquePath {
		t.Fatalf("默认路径 = %q，期望 %q", p.MasquePath, defaultMasquePath)
	}
	if p.MasqueAuth != masqueAuthNone {
		t.Fatalf("默认鉴权 = %q，期望 %q", p.MasqueAuth, masqueAuthNone)
	}
	if p.MasqueState != masqueStateDisabled {
		t.Fatalf("默认状态 = %q，期望 disabled", p.MasqueState)
	}

	// 启用 MASQUE 但状态为空：自动进入 pending
	p = vpnNodeProtocols{MasqueEnabled: true}.normalized()
	if p.MasqueState != masqueStatePending {
		t.Fatalf("启用后状态 = %q，期望 pending", p.MasqueState)
	}

	// 关闭 MASQUE 但残留 ready：状态归零为 disabled
	p = vpnNodeProtocols{MasqueEnabled: false, MasqueState: masqueStateReady}.normalized()
	if p.MasqueState != masqueStateDisabled {
		t.Fatalf("关闭后状态 = %q，期望 disabled", p.MasqueState)
	}

	// 未知状态：归零，不 panic
	p = vpnNodeProtocols{MasqueEnabled: true, MasqueState: "weird"}.normalized()
	if p.MasqueState != masqueStatePending {
		t.Fatalf("未知状态归一 = %q，期望 pending", p.MasqueState)
	}
}

func TestNodeProtocolsProtocolList(t *testing.T) {
	// protocolIDs：客户端（task-32）按字符串数组判断节点支持哪些协议。
	ids := defaultNodeProtocols().protocolIDs()
	if len(ids) != 1 || ids[0] != vpnProtocolPrivate {
		t.Fatalf("默认 protocolIDs = %v，期望 [private]", ids)
	}
	ids = (vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true}).protocolIDs()
	if len(ids) != 2 || ids[0] != vpnProtocolPrivate || ids[1] != vpnProtocolMasque {
		t.Fatalf("启用 masque 后 protocolIDs = %v，期望 [private masque]", ids)
	}
	// 异常数据（两者都关）：兜底 private
	ids = (vpnNodeProtocols{}).protocolIDs()
	if len(ids) != 1 || ids[0] != vpnProtocolPrivate {
		t.Fatalf("两者都关时 protocolIDs = %v，期望兜底 [private]", ids)
	}

	// protocolDetails：至少 private enabled=true，MASQUE 附状态与标准路径
	list := defaultNodeProtocols().protocolDetails()
	if len(list) != 2 {
		t.Fatalf("protocolDetails 数量 = %d，期望 2", len(list))
	}
	if list[0]["id"] != vpnProtocolPrivate || list[0]["enabled"] != true {
		t.Fatalf("private 项 = %v", list[0])
	}
	if list[1]["id"] != vpnProtocolMasque || list[1]["enabled"] != false {
		t.Fatalf("masque 项 = %v", list[1])
	}
	if list[1]["state"] != masqueStateDisabled || list[1]["path"] != defaultMasquePath {
		t.Fatalf("masque 默认状态/路径 = %v", list[1])
	}

	// 启用且就绪：MASQUE 项反映状态
	list = (vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true, MasqueState: masqueStateReady}).protocolDetails()
	if list[1]["enabled"] != true || list[1]["state"] != masqueStateReady {
		t.Fatalf("masque 就绪项 = %v", list[1])
	}

	// 异常数据（两者都关）：兜底 private enabled=true
	list = (vpnNodeProtocols{}).protocolDetails()
	if list[0]["enabled"] != true {
		t.Fatalf("两者都关时应兜底 private：%v", list[0])
	}
}

func TestValidateNodeProtocols(t *testing.T) {
	ok := vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true, MasqueState: masqueStateReady, MasquePath: defaultMasquePath, MasqueAuth: masqueAuthNone}
	if _, err := validateNodeProtocols(ok); err != nil {
		t.Fatalf("合法协议能力不应报错：%v", err)
	}
	bad := []vpnNodeProtocols{
		{PrivateEnabled: false, MasqueEnabled: false},
		{PrivateEnabled: true, MasqueEnabled: true, MasquePath: "masque"},
		{PrivateEnabled: true, MasqueEnabled: true, MasquePath: "/masque"}, // 非 RFC 9484 标准前缀
		{PrivateEnabled: true, MasqueEnabled: true, MasquePath: "/a b"},    // 含空白
		{PrivateEnabled: true, MasqueEnabled: true, MasquePort: 70000},     // 端口越界
		{PrivateEnabled: true, MasqueEnabled: true, MasqueAuth: "basic"},   // 鉴权枚举
		{PrivateEnabled: true, MasqueEnabled: true, MasqueState: "weird"},  // 状态枚举
	}
	for _, p := range bad {
		if _, err := validateNodeProtocols(p); err == nil {
			t.Fatalf("非法协议能力应报错：%+v", p)
		}
	}
}

func TestMergeNodeProtocols(t *testing.T) {
	no := false
	yes := true
	port := 8443

	// 新建未传协议字段：默认仅 private
	p, err := mergeNodeProtocols(defaultNodeProtocols(), vpnNodeReq{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.PrivateEnabled || p.MasqueEnabled {
		t.Fatalf("新建默认 = %+v，期望 private enabled / masque disabled", p)
	}

	// 新建显式启用 MASQUE：状态自动 pending
	p, err = mergeNodeProtocols(defaultNodeProtocols(), vpnNodeReq{MasqueEnabled: &yes, MasquePort: &port}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.MasqueEnabled || p.MasqueState != masqueStatePending || p.MasquePort != 8443 {
		t.Fatalf("新建 masque = %+v", p)
	}

	// 更新未传协议字段：保持原值
	cur := vpnNodeProtocols{PrivateEnabled: true, MasqueEnabled: true, MasqueState: masqueStateReady, MasquePath: defaultMasquePath}
	p, err = mergeNodeProtocols(cur, vpnNodeReq{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.MasqueState != masqueStateReady || p.MasquePath != defaultMasquePath {
		t.Fatalf("更新未传字段不应变化：%+v", p)
	}

	// 更新关闭私有协议但 MASQUE 未启用：应报错（至少一种协议）
	if _, err := mergeNodeProtocols(cur, vpnNodeReq{PrivateEnabled: &no, MasqueEnabled: &no}, false); err == nil {
		t.Fatal("两种协议都关闭时应报错")
	}

	// 更新手动标记 ready
	p, err = mergeNodeProtocols(cur, vpnNodeReq{MasqueState: masqueStateError}, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.MasqueState != masqueStateError {
		t.Fatalf("手动状态 = %q，期望 error", p.MasqueState)
	}
}

func TestMasqueEndpoint(t *testing.T) {
	node := &vpn.NodeRecord{Endpoint: "1.2.3.4:443", Domain: "vpn.example.com", ListenPort: 443}
	if got := masqueEndpoint(node, vpnNodeProtocols{MasquePort: 0}); got != "vpn.example.com:443" {
		t.Fatalf("默认 endpoint = %q", got)
	}
	if got := masqueEndpoint(node, vpnNodeProtocols{MasquePort: 8443}); got != "vpn.example.com:8443" {
		t.Fatalf("自定义端口 endpoint = %q", got)
	}
	// 没有域名时回退到接入地址 host
	node2 := &vpn.NodeRecord{Endpoint: "5.6.7.8:4443", ListenPort: 4443}
	if got := masqueEndpoint(node2, vpnNodeProtocols{}); got != "5.6.7.8:4443" {
		t.Fatalf("无域名 endpoint = %q", got)
	}
	if got := masqueServerName(node); got != "vpn.example.com" {
		t.Fatalf("server name = %q", got)
	}
	if got := masqueServerName(node2); got != "5.6.7.8" {
		t.Fatalf("无域名 server name = %q", got)
	}
	// 结果必须是合法 host:port
	if _, _, err := net.SplitHostPort(masqueEndpoint(node, vpnNodeProtocols{})); err != nil {
		t.Fatalf("endpoint 不是合法 host:port：%v", err)
	}
}
