package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFirstCommandWord(t *testing.T) {
	cases := map[string]string{
		"sing-box check -c /etc/sing-box/sb.json":        "sing-box",
		"xray run -test -config /etc/sing-box/xray.json": "xray",
		"/usr/local/bin/sing-box check":                  "sing-box",
		"":                                               "",
		"sh -c 'true'":                                   "",
	}
	for in, want := range cases {
		if got := firstCommandWord(in); got != want {
			t.Fatalf("firstCommandWord(%q)=%q, 期望 %q", in, got, want)
		}
	}
}

func TestEnsureComponentRejectsUnknownBinary(t *testing.T) {
	r := &Runner{}
	code, out := r.ensureComponent(context.Background(), "nginx")
	if code == 0 || out == "" {
		t.Fatalf("未白名单的组件应拒绝：code=%d out=%s", code, out)
	}
}

func TestInstallSingboxFromRelease(t *testing.T) {
	// 构造一个只有 sing-box 文件的 tar.gz
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho fake sing-box\n")
	if err := tw.WriteHeader(&tar.Header{Name: "sing-box-1.14.4-linux-amd64/sing-box", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	oldBase, oldVer, oldDir := singboxReleaseBase, singboxVersion, componentBinDir
	singboxReleaseBase, singboxVersion, componentBinDir = srv.URL, "1.14.4", t.TempDir()
	defer func() { singboxReleaseBase, singboxVersion, componentBinDir = oldBase, oldVer, oldDir }()

	r := &Runner{}
	code, out := r.installSingbox(context.Background())
	if code != 0 {
		t.Fatalf("安装失败：%d %s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(componentBinDir, "sing-box"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("二进制未写入：err=%v len=%d", err, len(got))
	}
}

func TestInstallMtgFromRelease(t *testing.T) {
	// 构造一个带 mtg-multi 二进制的 tar.gz（release 资产结构：<dir>/mtg-multi）
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho fake mtg-multi\n")
	if err := tw.WriteHeader(&tar.Header{Name: "mtg-multi-1.13.0-linux-amd64/mtg-multi", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.13.0/mtg-multi-1.13.0-linux-amd64.tar.gz" {
			t.Fatalf("下载地址不对：%s", r.URL.Path)
		}
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	oldBase, oldVer, oldDir := mtgReleaseBase, mtgVersion, componentBinDir
	mtgReleaseBase, mtgVersion, componentBinDir = srv.URL, "1.13.0", t.TempDir()
	defer func() { mtgReleaseBase, mtgVersion, componentBinDir = oldBase, oldVer, oldDir }()

	r := &Runner{}
	code, out := r.installMtg(context.Background())
	if code != 0 {
		t.Fatalf("安装失败：%d %s", code, out)
	}
	got, err := os.ReadFile(filepath.Join(componentBinDir, "mtg-multi"))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("二进制未写入：err=%v len=%d", err, len(got))
	}
}

func TestSameVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.14.4", "1.14.4", true},
		{"v1.14.4", "1.14.4", true},
		{"V1.14.4", "1.14.4", true},
		{"1.14.4", "1.15.0-alpha.13", false},
		{"", "1.14.4", false},
		{"1.14.4", "", false},
	}
	for _, c := range cases {
		if got := sameVersion(c.a, c.b); got != c.want {
			t.Fatalf("sameVersion(%q,%q)=%v, 期望 %v", c.a, c.b, got, c.want)
		}
	}
}
