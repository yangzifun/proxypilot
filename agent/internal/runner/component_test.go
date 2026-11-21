package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proxypilot-agent/internal/metrics"
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

func TestInstallSingboxRejectsNonS3Source(t *testing.T) {
	// 非 S3 组件基址必须 fail-closed，不再回落 GitHub。
	r := &Runner{ComponentBase: "https://github.com/SagerNet/sing-box"}
	code, out := r.installSingboxVersion(context.Background(), "1.14.4")
	if code == 0 {
		t.Fatalf("第三方来源不应安装成功：%s", out)
	}
	if !strings.Contains(out, "不会回落 GitHub") {
		t.Fatalf("错误信息应说明不会回落 GitHub：%s", out)
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

// 自建 sing-box 的 `version` 可能是 unknown；解析函数应回退到安装标记，
// 正常语义化版本输出优先于标记。用纯函数覆盖，避免测试依赖真实二进制/平台。
func TestInstalledVersionFallsBackToMarker(t *testing.T) {
	oldMarker := metrics.SingboxVersionMarker
	marker := filepath.Join(t.TempDir(), ".proxypilot-version")
	metrics.SingboxVersionMarker = marker
	defer func() { metrics.SingboxVersionMarker = oldMarker }()

	if got := versionWithMarkerFallback("sing-box", "sing-box version unknown\nTags: with_quic,with_v2ray_api\n"); got != "" {
		t.Fatalf("无标记时 unknown 应返回空，got %q", got)
	}
	if err := os.WriteFile(marker, []byte("1.15.0-alpha.13\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := versionWithMarkerFallback("sing-box", "sing-box version unknown\n"); got != "1.15.0-alpha.13" {
		t.Fatalf("unknown 应回退标记，got %q", got)
	}
	if got := versionWithMarkerFallback("sing-box", "sing-box version 1.14.4\n"); got != "1.14.4" {
		t.Fatalf("正常版本应优先，got %q", got)
	}
	if got := versionWithMarkerFallback("mtg-multi", "unknown"); got != "" {
		t.Fatalf("非 sing-box 不应读取标记，got %q", got)
	}
}
func TestWriteSingboxVersionMarker(t *testing.T) {
	oldMarker := metrics.SingboxVersionMarker
	marker := filepath.Join(t.TempDir(), "nested", "singbox-version")
	metrics.SingboxVersionMarker = marker
	defer func() { metrics.SingboxVersionMarker = oldMarker }()

	if err := writeSingboxVersionMarker("v1.14.4"); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(got)) != "1.14.4" {
		t.Fatalf("marker = %q err=%v", got, err)
	}
	if err := writeSingboxVersionMarker(""); err == nil {
		t.Fatal("空版本应报错")
	}
}

// 安装成功后必须写入版本标记，供 version 输出 unknown 的节点回退展示。
func TestInstallSingboxWritesVersionMarker(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	payload := []byte("#!/bin/sh\necho 'sing-box version unknown'\n")
	if err := tw.WriteHeader(&tar.Header{Name: "sing-box-1.14.4-linux-amd64/sing-box", Mode: 0o755, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	oldHTTP := componentHTTP
	componentHTTP = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(buf.Bytes())), Header: make(http.Header)}, nil
	})}
	defer func() { componentHTTP = oldHTTP }()

	oldDir := componentBinDir
	componentBinDir = t.TempDir()
	defer func() { componentBinDir = oldDir }()
	oldMarker := metrics.SingboxVersionMarker
	marker := filepath.Join(t.TempDir(), ".proxypilot-version")
	metrics.SingboxVersionMarker = marker
	defer func() { metrics.SingboxVersionMarker = oldMarker }()

	r := &Runner{ComponentBase: singboxComponentBase}
	code, out := r.installSingboxVersion(context.Background(), "1.14.4")
	if code != 0 {
		t.Fatalf("install = %d %s", code, out)
	}
	got, err := os.ReadFile(marker)
	if err != nil || strings.TrimSpace(string(got)) != "1.14.4" {
		t.Fatalf("marker = %q err=%v", got, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
