package client

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestParseInstallVersions(t *testing.T) {
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	line := strings.Join([]string{
		"7", "deploy", "3", b64("n"), b64("/p"), b64("c"), "abc", "0",
		b64(""), b64(""), b64(""), "1", b64(""), "0", b64(`{"sing-box":"1.14.4","xray":"25.9.11"}`),
	}, "|")
	a, ok := ParseActionLine(line)
	if !ok {
		t.Fatalf("解析失败")
	}
	if a.InstallVersions["sing-box"] != "1.14.4" || a.InstallVersions["xray"] != "25.9.11" {
		t.Fatalf("install_versions=%v", a.InstallVersions)
	}
	// 老 Worker 只有 14 个字段：不应报错，版本为空
	old := strings.Join([]string{
		"7", "deploy", "3", b64("n"), b64("/p"), b64("c"), "abc", "0",
		b64(""), b64(""), b64(""), "1", b64(""), "0",
	}, "|")
	if a2, ok2 := ParseActionLine(old); !ok2 || len(a2.InstallVersions) != 0 {
		t.Fatalf("老格式兼容失败：ok=%v vers=%v", ok2, a2.InstallVersions)
	}
}
