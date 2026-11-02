package api

import (
	"strings"
	"testing"
)

func TestClashTemplateInjectionCRLF(t *testing.T) {
	template := "mixed-port: 7890\r\n\r\nproxies:\r\n  - {name: 直连, type: direct}\r\n\r\nproxy-groups:\r\n  - name: PROXY\r\n    type: select\r\n\r\nrules:\r\n  - MATCH,PROXY\r\n"
	proxies := []map[string]any{
		{"name": "直连", "type": "direct"},
		{"name": "A", "port": 443, "type": "vless"},
	}
	out := injectClashProxies(template, proxies)
	if strings.Count(out, "proxies:") != 1 {
		t.Fatalf("CRLF 模板出现重复 proxies 键：\n%s", out)
	}
	if !strings.Contains(out, `{"name":"A","port":443,"type":"vless"}`) {
		t.Fatalf("CRLF 模板未注入节点：\n%s", out)
	}
	if strings.Contains(out, "# ===== 生成的节点配置 =====") {
		t.Fatalf("找到 proxies 时不应追加第二个节点块：\n%s", out)
	}
}
