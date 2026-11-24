package api

import (
	"strings"
	"testing"
)

const testTLSNodeDomain = "hk1-node.yzfn.top"

func testTLSUsers() []sbUser {
	return []sbUser{{Name: "u1", UUID: "11111111-2222-3333-4444-555555555555", SSPassword: "p1"}}
}

func testTLSTemplate() *singboxTemplate {
	return &singboxTemplate{
		Name: "t", PortBase: 13700, WarpBase: 13800,
		CertPath: "/etc/sing-box/cert.crt", KeyPath: "/etc/sing-box/private.key",
		EnableAnyTLS: 1, EnableHY2: 1, EnableVMess: 1, EnableVLWS: 1, EnableMasque: 1,
		ServerName: testTLSNodeDomain,
	}
}

func testInboundByTag(t *testing.T, cfg map[string]any, tag string) map[string]any {
	t.Helper()
	inbounds, _ := cfg["inbounds"].([]any)
	for _, raw := range inbounds {
		ib, _ := raw.(map[string]any)
		if ib["tag"] == tag {
			return ib
		}
	}
	t.Fatalf("missing inbound %s", tag)
	return nil
}

func TestSingboxTLSServerNameFromNodeDomain(t *testing.T) {
	tpl := testTLSTemplate()
	cfg := singboxRenderSb(tpl, testTLSUsers(), singboxPorts(tpl), "", false, "")
	for _, tag := range []string{"anytls-sb", "hy2-sb", "tuic-sb", "vless-ws-in"} {
		ib := testInboundByTag(t, cfg, tag)
		tls, _ := ib["tls"].(map[string]any)
		if got := strings.TrimSpace(jsonStr(tls["server_name"])); got != testTLSNodeDomain {
			t.Fatalf("%s server_name = %q, want %q", tag, got, testTLSNodeDomain)
		}
	}
	endpoints, _ := cfg["endpoints"].([]any)
	if len(endpoints) != 1 {
		t.Fatalf("endpoints = %#v", endpoints)
	}
	mq, _ := endpoints[0].(map[string]any)
	tls, _ := mq["tls"].(map[string]any)
	if got := strings.TrimSpace(jsonStr(tls["server_name"])); got != testTLSNodeDomain {
		t.Fatalf("masque server_name = %q, want %q", got, testTLSNodeDomain)
	}
}

func TestSingboxRenderBundleInjectNodeDomain(t *testing.T) {
	tpl := testTLSTemplate()
	tpl.ServerName = "" // bundle must inject from node.Domain
	tpl.UsersJSON = `[{"name":"u1","uuid":"11111111-2222-3333-4444-555555555555","ss_password":"p1"}]`
	files, _, _, msg := singboxRenderBundle(tpl, singboxRenderNode{Name: "hk1", Domain: testTLSNodeDomain}, serverRow{}, nil)
	if msg != "" {
		t.Fatalf("renderBundle msg = %s", msg)
	}
	if len(files) != 1 {
		t.Fatalf("files = %#v", files)
	}
	content, _ := files[0]["content"].(string)
	if !strings.Contains(content, `"server_name": "`+testTLSNodeDomain+`"`) {
		t.Fatalf("sb.json missing node domain server_name: %s", content)
	}
}

func TestSingboxPerUserTLSServerName(t *testing.T) {
	tpl := testTLSTemplate()
	tpl.EnableMasque = 0
	tpl.EnableVMess = 0
	tpl.EnableVLWS = 0
	users := testTLSUsers()
	ports := map[string]int{users[0].UUID: 14000}
	out := singboxPerUserInbounds(tpl, users, ports, "")
	if len(out) != 2 {
		t.Fatalf("per-user inbounds = %d, want 2", len(out))
	}
	for _, raw := range out {
		ib, _ := raw.(map[string]any)
		tls, _ := ib["tls"].(map[string]any)
		if got := strings.TrimSpace(jsonStr(tls["server_name"])); got != testTLSNodeDomain {
			t.Fatalf("%s server_name = %q", ib["tag"], got)
		}
	}
}

func TestSingboxRenderEmptyDomainOmitsServerName(t *testing.T) {
	tpl := testTLSTemplate()
	tpl.ServerName = ""
	cfg := singboxRenderSb(tpl, testTLSUsers(), singboxPorts(tpl), "", false, "")
	for _, tag := range []string{"anytls-sb", "hy2-sb", "tuic-sb", "vless-ws-in"} {
		ib := testInboundByTag(t, cfg, tag)
		tls, _ := ib["tls"].(map[string]any)
		if _, ok := tls["server_name"]; ok {
			t.Fatalf("%s should omit server_name when node domain is empty", tag)
		}
	}
}

func TestSingboxClientLinksTLSNoInsecure(t *testing.T) {
	tpl := testTLSTemplate()
	node := singboxNode{Name: "hk1", Domain: testTLSNodeDomain}
	links := singboxClientLinks(tpl, node, testTLSUsers())
	want := map[string]bool{
		"anytls": false, "hysteria2": false, "tuic": false, "vless-ws": false,
	}
	for _, link := range links {
		proto := mapStr(link, "protocol", "")
		if _, ok := want[proto]; !ok {
			continue
		}
		raw := mapStr(link, "link", "")
		want[proto] = true
		if strings.Contains(raw, "insecure") || strings.Contains(raw, "allow_insecure") {
			t.Fatalf("%s link still contains insecure/allow_insecure: %s", proto, raw)
		}
		if !strings.Contains(raw, "sni="+testTLSNodeDomain) {
			t.Fatalf("%s link sni is not node domain: %s", proto, raw)
		}
	}
	for proto, seen := range want {
		if !seen {
			t.Fatalf("missing %s link", proto)
		}
	}
}

func TestSingboxClientLinksEmptyDomainSkipsTLS(t *testing.T) {
	tpl := testTLSTemplate()
	node := singboxNode{Name: "hk1"}
	links := singboxClientLinks(tpl, node, testTLSUsers())
	for _, link := range links {
		proto := mapStr(link, "protocol", "")
		switch proto {
		case "anytls", "hysteria2", "tuic", "vless-ws":
			t.Fatalf("empty domain must not generate fake TLS link: %s %s", proto, mapStr(link, "link", ""))
		}
	}
}
