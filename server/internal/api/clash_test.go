package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestClashConversionGolden 用 worker.js 生成的黄金用例逐协议比对转换结果。
// 重新生成：node workers/gen_clash_fixtures.mjs
func TestClashConversionGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "clash", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("未找到黄金用例: %v", err)
	}
	for _, file := range files {
		file := file
		t.Run(filepath.Base(file), func(t *testing.T) {
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Input   string            `json:"input"`
				Proxies []json.RawMessage `json:"proxies"`
			}
			if err := json.Unmarshal(raw, &fixture); err != nil {
				t.Fatalf("解析用例失败: %v", err)
			}
			if len(fixture.Proxies) < 2 {
				t.Fatalf("用例缺少节点: %d", len(fixture.Proxies))
			}

			var want any
			if err := json.Unmarshal(fixture.Proxies[1], &want); err != nil {
				t.Fatal(err)
			}
			got := convertToClashFormat(fixture.Input)
			if got == nil {
				t.Fatal(" convertToClashFormat 返回 nil")
			}
			gotJSON := canonicalJSON(got)
			wantJSON := canonicalJSON(want)
			if gotJSON != wantJSON {
				t.Fatalf("转换结果不一致\n got: %s\nwant: %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestClashTemplateInjection(t *testing.T) {
	template := "mixed-port: 7890\n\nproxies:\n\nproxy-groups:\n  - name: PROXY\n    type: select\n\nrules:\n  - MATCH,PROXY\n"
	proxies := []map[string]any{
		{"name": "直连", "type": "direct"},
		{"name": "A", "type": "vless", "port": 443},
	}
	out := injectClashProxies(template, proxies)
	if !contains(out, "proxies:\n  - {\"name\":\"直连\",\"type\":\"direct\"}\n  - {\"name\":\"A\",\"port\":443,\"type\":\"vless\"}\nproxy-groups:") {
		t.Fatalf("注入结果不符合预期:\n%s", out)
	}
}

func TestClashUnsupportedReturnsNil(t *testing.T) {
	if got := convertToClashFormat("vmess://" + "%%%"); got != nil {
		t.Fatal("非法 vmess 应返回 nil")
	}
	if got := convertToClashFormat("vless://uuid@example.com:443?type=quic&security=tls"); got != nil {
		t.Fatal("QUIC 传输应返回 nil")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
