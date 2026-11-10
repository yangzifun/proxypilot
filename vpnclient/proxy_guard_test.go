package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 回归守卫：VPN 客户端的控制面请求绝不能经过系统/本地代理。
//
// 真实事故：listNodes / connect / client-auth / 拉平台 CA 都用 http.DefaultClient，
// 而它走 ProxyFromEnvironment。用户机器上开着 Clash（127.0.0.1:7890）时，
// 一旦代理没启动或代理本身依赖 VPN 出网，就形成死循环 ——
// 表现为「获取不到节点信息」「TLS handshake timeout」，
// 但服务端日志里一条入站请求都没有。
func TestNoProxyInControlPlane(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{
		"http.DefaultClient",
		"http.ProxyFromEnvironment",
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// 去掉注释再判断，避免注释里提到这些名字就误报
		var code strings.Builder
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			code.WriteString(line)
			code.WriteString("\n")
		}
		src := code.String()
		for _, bad := range forbidden {
			if strings.Contains(src, bad) {
				t.Errorf("%s 里出现了 %s：控制面请求必须用 panelHTTP（Proxy=nil）或 loopbackHTTP", f, bad)
			}
		}
	}
}

// panelHTTP 与 loopbackHTTP 必须显式禁用代理
func TestControlPlaneClientsHaveNoProxy(t *testing.T) {
	if panelHTTP.Transport == nil {
		t.Fatal("panelHTTP 没有自定义 Transport")
	}
	tr, ok := panelHTTP.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("panelHTTP.Transport 类型不符：%T", panelHTTP.Transport)
	}
	if tr.Proxy != nil {
		t.Fatal("panelHTTP 必须 Proxy=nil，否则会走本地代理")
	}
	if panelHTTP.Timeout <= 0 {
		t.Fatal("panelHTTP 必须有超时")
	}
}