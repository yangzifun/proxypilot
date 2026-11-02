package api

import (
	"net"
	"net/http"
	"testing"
)

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.0.0.1", "172.16.1.1", "192.168.1.1",
		"169.254.169.254", "100.64.0.1", "0.0.0.0", "192.0.2.1",
		"198.18.0.1", "198.51.100.1", "203.0.113.1", "240.0.0.1",
		"fc00::1", "fe80::1", "ff02::1",
	}
	for _, s := range blocked {
		if !isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s 应被判定为禁止访问", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"} {
		if isBlockedIP(net.ParseIP(s)) {
			t.Errorf("%s 不应被判定为禁止访问", s)
		}
	}
}

func TestValidSubSourceURLRejectsInternalTargets(t *testing.T) {
	bad := []string{
		"http://127.0.0.1/sub",
		"http://[::1]/sub",
		"http://169.254.169.254/latest/meta-data",
		"http://100.64.0.1/sub",
		"http://user@127.0.0.1/sub",
		"http://localhost/sub",
		"http://foo.local/sub",
		"http://foo.internal/sub",
		"ftp://example.com/sub",
	}
	for _, u := range bad {
		if _, ok := validSubSourceURL(u); ok {
			t.Errorf("%s 应被拒绝", u)
		}
	}
	good := []string{
		"https://example.com/sub",
		"http://1.1.1.1/sub",
		"https://sub.example.com:8443/path?x=1",
	}
	for _, u := range good {
		if _, ok := validSubSourceURL(u); !ok {
			t.Errorf("%s 应被允许", u)
		}
	}
}

func TestSubSourceClientRejectsRedirectToLoopback(t *testing.T) {
	client := subSourceHTTPClient()
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:20241/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, []*http.Request{req}); err == nil {
		t.Fatal("重定向到 loopback 应被拒绝")
	}
}
