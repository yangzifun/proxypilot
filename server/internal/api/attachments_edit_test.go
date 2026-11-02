package api

import "testing"

func TestAttachmentEditableAndCDN(t *testing.T) {
	if !attachmentEditable("text/plain", "a.txt", []byte("hello")) {
		t.Fatal("text/plain 应可编辑")
	}
	if !attachmentEditable("application/octet-stream", "config.yaml", []byte("a: 1")) {
		t.Fatal(".yaml 应可编辑")
	}
	if attachmentEditable("application/octet-stream", "binary.bin", []byte{0xff, 0x00}) {
		t.Fatal("二进制不应可编辑")
	}
	if got := objectCDNURL("https://cdn.example.com/", "agent/中文 文件.txt"); got != "https://cdn.example.com/agent/%E4%B8%AD%E6%96%87%20%E6%96%87%E4%BB%B6.txt" {
		t.Fatalf("cdn url = %q", got)
	}
	if got := objectCDNURL("", "a.txt"); got != "" {
		t.Fatalf("空 CDN 应返回空，得到 %q", got)
	}
}
