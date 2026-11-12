package api

import (
	"strings"
	"testing"
)

// 安装命令必须使用「设定的分发基址」下的 install.sh，
// 不能退化成内网地址（新机器还没接入内网，拉不到脚本）。
func TestBuildInstallCommand(t *testing.T) {
	const tok = "ag_test"

	// 1) 设定了分发基址：脚本走桶路径，--url 用伺服地址
	got := buildInstallCommand("https://s3.example.com/agent/", "https://pp.example.com", tok)
	want := "curl -fsSL https://s3.example.com/agent/install.sh | sudo sh -s -- --key ag_test --url https://pp.example.com"
	if got != want {
		t.Fatalf("带基址\n  got: %s\n want: %s", got, want)
	}

	// 2) 未设定基址：回落面板自身
	got = buildInstallCommand("", "https://pp.example.com/", tok)
	want = "curl -fsSL https://pp.example.com/api/agent/install.sh | sudo sh -s -- --key ag_test --url https://pp.example.com"
	if got != want {
		t.Fatalf("无基址\n  got: %s\n want: %s", got, want)
	}
}

// 内网地址不能被当成安装命令里的伺服地址。
func TestIsPrivateHost(t *testing.T) {
	cases := map[string]bool{
		"http://100.96.0.18:80":    true,
		"http://127.0.0.1:8080":    true,
		"http://localhost:9000":    true,
		"http://10.1.2.3":          true,
		"https://pp.example.com":   false,
		"https://s3.example.com/x": false,
	}
	for in, want := range cases {
		if got := isPrivateHost(in); got != want {
			t.Fatalf("isPrivateHost(%q) = %v, 期望 %v", in, got, want)
		}
	}
}
// 安装命令里绝不能出现内网/组网地址：新节点此时还没组网，连不上就装不了。
func TestInstallCommandNeverHasPrivateHost(t *testing.T) {
	origin := "https://proxypilot.yangzifun.org"
	cmd := buildInstallCommand("https://s3.proxypilot.yangzifun.org/agent", origin, "ag_x")
	for _, bad := range []string{"100.96.", "127.0.0.1", "localhost", "10.0.0."} {
		if strings.Contains(cmd, bad) {
			t.Fatalf("安装命令包含内网地址 %q：%s", bad, cmd)
		}
	}
	if !strings.Contains(cmd, "https://s3.proxypilot.yangzifun.org/agent/install.sh") {
		t.Fatalf("脚本地址应为设定的分发基址：%s", cmd)
	}
}