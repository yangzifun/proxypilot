package main

import (
	"path/filepath"
	"testing"
)

func TestNormalizeProtocol(t *testing.T) {
	cases := map[string]string{
		"":           protocolAuto,
		"auto":       protocolAuto,
		"AUTO":       protocolAuto,
		"private":    protocolPrivate,
		"private ":   protocolPrivate,
		"tunnel":     protocolPrivate,
		"pilotlink":  protocolPrivate,
		"PilotLink":  protocolPrivate,
		"pilot-link": protocolPrivate,
		"masque":     protocolMasque,
		"MQ":         protocolMasque,
		"connect-ip": protocolMasque,
		"connectip":  protocolMasque,
		"unknown-xx": protocolAuto,
	}
	for in, want := range cases {
		if got := normalizeProtocol(in); got != want {
			t.Fatalf("normalizeProtocol(%q)=%q，期望 %q", in, got, want)
		}
	}
}

func TestTransportOrder(t *testing.T) {
	cases := []struct {
		name string
		pref string
		node node
		want []string
	}{
		{"auto 旧面板（未知）", "auto", node{}, []string{protocolPrivate, protocolMasque}},
		{"auto 节点声明双协议", "auto", node{Protocols: []string{"private", "masque"}}, []string{protocolPrivate, protocolMasque}},
		{"auto 节点仅私有", "auto", node{Protocols: []string{"private"}}, []string{protocolPrivate}},
		{"显式私有", "private", node{Protocols: []string{"masque"}}, []string{protocolPrivate}},
		{"显式 MASQUE", "masque", node{Protocols: []string{"private", "masque"}}, []string{protocolMasque}},
		{"空值按自动", "", node{}, []string{protocolPrivate, protocolMasque}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := transportOrder(c.pref, c.node)
			if len(got) != len(c.want) {
				t.Fatalf("transportOrder(%q,%v)=%v，期望 %v", c.pref, c.node.Protocols, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("transportOrder(%q,%v)=%v，期望 %v", c.pref, c.node.Protocols, got, c.want)
				}
			}
		})
	}
}

func TestNodeSupportsMasque(t *testing.T) {
	if !nodeSupportsMasque(node{}) {
		t.Fatal("旧面板（未返回 protocols）应按未知处理，允许尝试 MASQUE")
	}
	if !nodeSupportsMasque(node{Protocols: []string{"private", "masque"}}) {
		t.Fatal("声明 masque 的节点应支持")
	}
	if nodeSupportsMasque(node{Protocols: []string{"private"}}) {
		t.Fatal("仅声明 private 的节点不应支持 MASQUE")
	}
}

func TestMasquePathDefault(t *testing.T) {
	if got := masquePath(nil); got != "/.well-known/masque/ip/0.0.0.0/0/" {
		t.Fatalf("默认 MASQUE path=%q", got)
	}
	if got := masquePath(&profile{MasquePath: "/custom/path/"}); got != "/custom/path/" {
		t.Fatalf("面板下发 path 未生效：%q", got)
	}
}

func TestProtocolLabel(t *testing.T) {
	if protocolLabel("masque") != "MASQUE" || protocolLabel("private") != "PilotLink" || protocolLabel("auto") != "自动" {
		t.Fatalf("协议展示名异常：%q %q %q", protocolLabel("masque"), protocolLabel("private"), protocolLabel("auto"))
	}
}

func TestProtocolSettingRoundTrip(t *testing.T) {
	prev := configPathOverride
	configPathOverride = filepath.Join(t.TempDir(), "ppvpn-config.json")
	defer func() { configPathOverride = prev }()

	if err := saveSettings(uiSettings{API: "https://panel.example.com", Protocol: protocolMasque}); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	loaded := loadSettings(uiSettings{API: "https://default.example.com"})
	if got := normalizeProtocol(loaded.Protocol); got != protocolMasque {
		t.Fatalf("protocol 未持久化：%q", loaded.Protocol)
	}
	// 旧配置无 protocol 字段时回落到默认（auto）。
	if err := saveSettings(uiSettings{API: "https://panel.example.com"}); err != nil {
		t.Fatalf("保存配置失败：%v", err)
	}
	loaded = loadSettings(uiSettings{API: "https://default.example.com"})
	if got := normalizeProtocol(loaded.Protocol); got != protocolAuto {
		t.Fatalf("旧配置 protocol 默认值异常：%q", loaded.Protocol)
	}
}
