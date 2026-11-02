package api

import "testing"

func TestParseUUIDList(t *testing.T) {
	if got := parseUUIDList(`["a","b",""]`); len(got) != 2 || got[0] != "a" {
		t.Fatalf("got %#v", got)
	}
	if got := parseUUIDList(`not-json`); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
	if got := parseUUIDList(``); len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestNormalizeNodeConfigInput(t *testing.T) {
	if _, msg := normalizeNodeConfigInput(map[string]any{"name": "x"}); msg == "" {
		t.Fatal("缺少 server_id 应报错")
	}
	if _, msg := normalizeNodeConfigInput(map[string]any{"name": "x", "server_id": float64(1)}); msg == "" {
		t.Fatal("缺少内容/模板应报错")
	}
	in, msg := normalizeNodeConfigInput(map[string]any{
		"name": "x", "server_id": float64(1), "content": "abc", "target_path": "/etc/x.conf",
	})
	if msg != "" {
		t.Fatalf("msg = %s", msg)
	}
	if in["name"] != "x" || in["enabled"].(int64) != 1 {
		t.Fatalf("input = %#v", in)
	}
}
