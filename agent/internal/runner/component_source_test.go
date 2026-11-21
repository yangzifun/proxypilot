package runner

import "testing"

func TestIsAllowedSingboxDownloadURL(t *testing.T) {
	allowed := []string{
		"https://s3.proxypilot.yangzifun.org/singbox/sing-box-1.14.4-linux-amd64.tar.gz",
	}
	for _, u := range allowed {
		if !isAllowedSingboxDownloadURL(u) {
			t.Fatalf("%q 应允许", u)
		}
	}
	denied := []string{
		"https://github.com/SagerNet/sing-box/releases/download/v1.14.4/x.tar.gz",
		"https://s3.proxypilot.yangzifun.org/agent-dist/sing-box.tar.gz",
		"http://s3.proxypilot.yangzifun.org/singbox/x.tar.gz",
		"https://s3.proxypilot.yangzifun.org/singbox/../agent-dist/x",
		"",
	}
	for _, u := range denied {
		if isAllowedSingboxDownloadURL(u) {
			t.Fatalf("%q 应拒绝", u)
		}
	}
}

func TestSingboxDownloadURLsOnlyS3(t *testing.T) {
	r := &Runner{ComponentBase: "https://s3.proxypilot.yangzifun.org/singbox"}
	urls := r.singboxDownloadURLs("1.14.4", "amd64")
	if len(urls) != 1 || urls[0] != "https://s3.proxypilot.yangzifun.org/singbox/sing-box-1.14.4-linux-amd64.tar.gz" {
		t.Fatalf("urls = %#v", urls)
	}
	r.ComponentBase = "https://github.com/SagerNet/sing-box"
	if urls := r.singboxDownloadURLs("1.14.4", "amd64"); len(urls) != 0 {
		t.Fatalf("第三方基址应返回空，got %#v", urls)
	}
	r.ComponentBase = ""
	if urls := r.singboxDownloadURLs("1.14.4", "arm64"); len(urls) != 0 {
		t.Fatalf("空基址应返回空，got %#v", urls)
	}
}
