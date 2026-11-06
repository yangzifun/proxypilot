package api

import (
	"strings"
	"testing"
)

func TestCDNPreferredExpandsLinks(t *testing.T) {
	tpl := &singboxTemplate{
		ID: 1, Name: "t", PortBase: 13700, EnableVLWS: 1,
		CertPath: "/etc/sing-box/cert.crt", KeyPath: "/etc/sing-box/private.key",
		ProtocolOptions: `{"vlws":{"path":"/vlws","cdn_prefer":{"enabled":1,"group":"default","mode":"expand"}}}`,
	}
	cdnPreferMu.Lock()
	old := cdnPreferCache
	cdnPreferCache = []cdnPrefer{
		{Address: "162.159.198.1", Port: 443, Group: "default", Enabled: 1},
		{Address: "yg1.ygkkk.dpdns.org", Port: 443, Group: "default", Enabled: 1},
	}
	cdnPreferMu.Unlock()
	defer func() {
		cdnPreferMu.Lock()
		cdnPreferCache = old
		cdnPreferMu.Unlock()
	}()

	node := singboxNode{Name: "hk", Domain: "hk.example.com", CDNDomain: "cdn.example.com"}
	links := singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})

	cdnLinks := []map[string]any{}
	for _, l := range links {
		if b, _ := l["cdn"].(bool); b {
			cdnLinks = append(cdnLinks, l)
		}
	}
	if len(cdnLinks) != 2 {
		t.Fatalf("期望 2 条 CDN 优选链接，实际 %d", len(cdnLinks))
	}
	joined := ""
	for _, l := range cdnLinks {
		s, _ := l["link"].(string)
		joined += s + "\n"
	}
	for _, want := range []string{"162.159.198.1:443", "yg1.ygkkk.dpdns.org:443"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少优选地址 %s:\n%s", want, joined)
		}
	}
	// SNI 与 Host 必须保持节点 CDN 域名
	if strings.Count(joined, "sni=cdn.example.com") != 2 || strings.Count(joined, "host=cdn.example.com") != 2 {
		t.Fatalf("SNI/Host 未保持 CDN 域名:\n%s", joined)
	}
	// 关闭优选时应回退为 cdn.example.com 单条
	tpl.ProtocolOptions = `{"vlws":{"path":"/vlws"}}`
	links = singboxClientLinks(tpl, node, []sbUser{{Name: "u1", UUID: "11111111-1111-4111-8111-111111111111"}})
	found := false
	for _, l := range links {
		if s, _ := l["link"].(string); strings.Contains(s, "@cdn.example.com:443") {
			found = true
		}
	}
	if !found {
		t.Fatal("未启用优选时应回退 cdn.example.com")
	}
}
