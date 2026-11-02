package api

import "testing"

func TestSafeObjectKey(t *testing.T) {
	good := []string{"agent/manifest.json", "rule/configs/a.yaml", "docs/中文 文件.txt"}
	for _, k := range good {
		if got, ok := safeObjectKey(k); !ok || got != k {
			t.Fatalf("safeObjectKey(%q) = %q,%v", k, got, ok)
		}
	}
	bad := []string{"", "../etc/passwd", "/", "a/../b", "a\x00b"}
	for _, k := range bad {
		if _, ok := safeObjectKey(k); ok {
			t.Fatalf("safeObjectKey(%q) 应失败", k)
		}
	}
	if got := normalizePrefix("agent"); got != "agent/" {
		t.Fatalf("normalizePrefix = %q", got)
	}
}
