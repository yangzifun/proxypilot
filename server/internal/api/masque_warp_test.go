package api

import (
	"strings"
	"testing"
)

func TestRenderMasqueWarpYAML(t *testing.T) {
	s := defaultMasqueWarpSettings()
	s.ServerIPs = []string{"162.159.198.2"}
	s.Ports = []int{443}
	s.DNS = []string{"1.1.1.1"}
	p := masqueWarpProfile{
		UUID:          "11111111-1111-4111-8111-111111111111",
		PeerPublicKey: "peer-pub",
		IPv4:          "172.16.0.2",
		IPv6:          "2606:4700::1",
	}
	y := renderMasqueWarpYAML(s, p, "priv-key")
	for _, want := range []string{
		"type: masque", "server: \"162.159.198.2\"", "private-key: \"priv-key\"",
		"public-key: \"peer-pub\"", "ip: 172.16.0.2", "ipv6: \"2606:4700::1\"",
		"sni: \"zt-masque.cloudflareclient.com\"", "MATCH,MASQUE",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("YAML 缺少 %q\n%s", want, y)
		}
	}
	if strings.Contains(y, "token") {
		t.Error("YAML 不应包含注册 token")
	}
}
