package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =====================================================================
//  最新版本查询单测：解析 / 降级 / 缓存 / 注入
// =====================================================================

func resetLatestVersionsCache() {
	latestVersionsCache.Lock()
	latestVersionsCache.at = time.Time{}
	latestVersionsCache.data = latestVersionsData{}
	latestVersionsCache.Unlock()
}

func fakeLatestHTTPResponse(status int, body string, headers map[string]string) *http.Response {
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    &http.Request{Method: http.MethodGet},
	}
}

func writeTempOSRelease(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "os-release")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入临时 os-release 失败：%v", err)
	}
	return path
}

func TestParseCloudflaredTagLocation(t *testing.T) {
	cases := []struct {
		location string
		want     string
		wantErr  bool
	}{
		{"https://github.com/cloudflare/cloudflared/releases/tag/2026.11.0", "2026.11.0", false},
		{"https://github.com/cloudflare/cloudflared/releases/tag/2026.11.0/", "2026.11.0", false},
		{"https://github.com/cloudflare/cloudflared/releases/tag/v2026.12.1", "2026.12.1", false},
		{"", "", true},
		{"https://github.com/cloudflare/cloudflared/releases/latest", "", true},
		{"https://github.com/other/repo/releases/tag/1.2.3", "", true},
		{"not-a-url", "", true},
	}
	for _, tc := range cases {
		got, err := parseCloudflaredTagLocation(tc.location)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("Location %q 应报错，得到 %q", tc.location, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("Location %q 解析 = %q err=%v，期望 %q", tc.location, got, err, tc.want)
		}
	}
}

func TestParseWarpPackagesVersion(t *testing.T) {
	body := `Package: cloudflare-warp
Version: 2026.8.2115.0
Architecture: amd64
Description: Cloudflare WARP client

Package: cloudflare-warp
Version: 2026.9.1
Architecture: amd64

Package: another-package
Version: 9.9.9
`
	got, err := parseWarpPackagesVersion(body, "cloudflare-warp")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got != "2026.9.1" {
		t.Fatalf("多 stanza 应取最大版本，得到 %q", got)
	}

	// CRLF + 单个 stanza：保留原始字符串（.0 后缀不裁剪）
	crlf := "Package: cloudflare-warp\r\nVersion: 2026.8.2115.0\r\n\r\n"
	got, err = parseWarpPackagesVersion(crlf, "cloudflare-warp")
	if err != nil || got != "2026.8.2115.0" {
		t.Fatalf("CRLF 解析 = %q err=%v", got, err)
	}

	// 大小写/空白容忍
	got, err = parseWarpPackagesVersion("Package:   cloudflare-warp  \nVersion:   2026.1.2.0   \n", "cloudflare-warp")
	if err != nil || got != "2026.1.2.0" {
		t.Fatalf("空白容忍解析 = %q err=%v", got, err)
	}

	// 包不存在 / 空 body
	if _, err := parseWarpPackagesVersion(body, "missing-package"); err == nil {
		t.Fatal("包不存在应报错")
	}
	if _, err := parseWarpPackagesVersion("  ", "cloudflare-warp"); err == nil {
		t.Fatal("空 Packages 应报错")
	}
}

func TestOSVersionCodename(t *testing.T) {
	path := writeTempOSRelease(t, `PRETTY_NAME="Ubuntu 26.04 LTS"
NAME="Ubuntu"
VERSION_CODENAME=resolute
UBUNTU_CODENAME=resolute
`)
	if got := osVersionCodename(path); got != "resolute" {
		t.Fatalf("codename = %q，期望 resolute", got)
	}
	// 只有 UBUNTU_CODENAME 时也能取；引号会被去掉
	path2 := writeTempOSRelease(t, "UBUNTU_CODENAME='noble'\n")
	if got := osVersionCodename(path2); got != "noble" {
		t.Fatalf("codename = %q，期望 noble", got)
	}
	// 文件不存在返回空
	if got := osVersionCodename(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("缺失文件应返回空，得到 %q", got)
	}
}

// WARP apt 版本比较：按数字段比较，尾随 .0 归一化（复用 agentVersionNewer）。
func TestWarpVersionCompare(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"2026.10.0", "2026.9.9", true},
		{"2026.8.2115.0", "2026.8.2115", false}, // 尾随 .0 视为相等
		{"2026.8.2115.1", "2026.8.2115.0", true},
		{"2026.8.2115.0", "2026.8.2115.0", false},
	}
	for _, tc := range cases {
		if got := agentVersionNewer(tc.latest, tc.current); got != tc.want {
			t.Fatalf("agentVersionNewer(%q,%q) = %v，期望 %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestFetchCloudflaredLatestFromRedirect(t *testing.T) {
	origDo := latestHTTPDo
	defer func() { latestHTTPDo = origDo }()
	var calls int32
	var sawUA string
	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		sawUA = req.Header.Get("User-Agent")
		if req.URL.String() == cloudflaredReleasesLatestURL {
			return fakeLatestHTTPResponse(http.StatusFound, "", map[string]string{
				"Location": "https://github.com/cloudflare/cloudflared/releases/tag/2026.11.0",
			}), nil
		}
		t.Fatalf("拿到版本后不应再请求 GitHub API：%s", req.URL)
		return nil, nil
	}
	item := fetchCloudflaredLatest(context.Background())
	if item.Latest != "2026.11.0" || item.Source != "github-release" || item.Error != "" {
		t.Fatalf("解析结果 = %+v", item)
	}
	if item.CheckedAt <= 0 {
		t.Fatalf("checked_at 未设置：%+v", item)
	}
	if calls != 1 {
		t.Fatalf("请求次数 = %d，期望 1", calls)
	}
	if sawUA == "" {
		t.Fatal("请求未设置 User-Agent")
	}
}

func TestFetchCloudflaredLatestAPIFallback(t *testing.T) {
	origDo := latestHTTPDo
	defer func() { latestHTTPDo = origDo }()
	var calls int32
	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		switch req.URL.String() {
		case cloudflaredReleasesLatestURL:
			return fakeLatestHTTPResponse(http.StatusInternalServerError, "boom", nil), nil
		case cloudflaredGitHubAPIURL:
			return fakeLatestHTTPResponse(http.StatusOK, `{"tag_name":"v2026.12.0"}`, nil), nil
		}
		t.Fatalf("未知请求：%s", req.URL)
		return nil, nil
	}
	item := fetchCloudflaredLatest(context.Background())
	if item.Latest != "2026.12.0" || item.Source != "github-api" || item.Error != "" {
		t.Fatalf("API 兜底结果 = %+v", item)
	}
	if calls != 2 {
		t.Fatalf("请求次数 = %d，期望 2", calls)
	}
}

func TestFetchCloudflaredLatestBothFailDegrades(t *testing.T) {
	origDo := latestHTTPDo
	defer func() { latestHTTPDo = origDo }()
	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}
	item := fetchCloudflaredLatest(context.Background())
	if item.Latest != "" || item.Error == "" {
		t.Fatalf("两项都失败时应 latest 为空且 error 非空：%+v", item)
	}
}

func TestFetchWarpLatestPrimaryCodename(t *testing.T) {
	origOS := latestOSReleaseFile
	origDo := latestHTTPDo
	defer func() { latestOSReleaseFile = origOS; latestHTTPDo = origDo }()
	latestOSReleaseFile = writeTempOSRelease(t, "VERSION_CODENAME=noble\n")

	var calls int32
	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		if !strings.Contains(req.URL.String(), "/dists/noble/main/binary-amd64/Packages") {
			t.Fatalf("主 codename 可用时不应请求兜底：%s", req.URL)
		}
		return fakeLatestHTTPResponse(http.StatusOK, "Package: cloudflare-warp\nVersion: 2026.8.2115.0\n", nil), nil
	}
	item := fetchWarpLatest(context.Background())
	if item.Latest != "2026.8.2115.0" || item.Source != "apt-cloudflare" || item.Error != "" {
		t.Fatalf("解析结果 = %+v", item)
	}
	if calls != 1 {
		t.Fatalf("请求次数 = %d，期望 1", calls)
	}
}

func TestFetchWarpLatestFallbackTakesMaxVersion(t *testing.T) {
	origOS := latestOSReleaseFile
	origDo := latestHTTPDo
	defer func() { latestOSReleaseFile = origOS; latestHTTPDo = origDo }()
	// 未知系统 codename → 走兜底列表
	latestOSReleaseFile = writeTempOSRelease(t, "VERSION_CODENAME=weirdos\n")

	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		switch {
		case strings.Contains(u, "/dists/resolute/"):
			return fakeLatestHTTPResponse(http.StatusOK, "Package: cloudflare-warp\nVersion: 2026.8.2115.0\n", nil), nil
		case strings.Contains(u, "/dists/noble/"):
			return fakeLatestHTTPResponse(http.StatusOK, "Package: cloudflare-warp\nVersion: 2026.8.2115.0\n", nil), nil
		case strings.Contains(u, "/dists/jammy/"):
			return fakeLatestHTTPResponse(http.StatusNotFound, "not found", nil), nil
		case strings.Contains(u, "/dists/focal/"):
			return fakeLatestHTTPResponse(http.StatusOK, "Package: cloudflare-warp\nVersion: 2026.10.1.0\n", nil), nil
		case strings.Contains(u, "/dists/weirdos/"):
			return fakeLatestHTTPResponse(http.StatusNotFound, "not found", nil), nil
		}
		t.Fatalf("未知请求：%s", u)
		return nil, nil
	}
	item := fetchWarpLatest(context.Background())
	if item.Latest != "2026.10.1.0" || item.Source != "apt-cloudflare" || item.Error != "" {
		t.Fatalf("兜底取最大版本结果 = %+v", item)
	}
}

func TestFetchWarpLatestAllFailDegrades(t *testing.T) {
	origOS := latestOSReleaseFile
	origDo := latestHTTPDo
	defer func() { latestOSReleaseFile = origOS; latestHTTPDo = origDo }()
	latestOSReleaseFile = writeTempOSRelease(t, "VERSION_CODENAME=resolute\n")
	latestHTTPDo = func(req *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}
	item := fetchWarpLatest(context.Background())
	if item.Latest != "" || item.Error == "" {
		t.Fatalf("全部失败时应 latest 为空且 error 非空：%+v", item)
	}
}

// 缓存命中 / refresh=1 强制刷新 / 单项失败降级（HTTP 仍 200）。
func TestLatestVersionsEndpointCacheAndRefresh(t *testing.T) {
	resetLatestVersionsCache()
	defer resetLatestVersionsCache()

	origCF, origWarp := latestCloudflaredFetch, latestWarpFetch
	defer func() { latestCloudflaredFetch, latestWarpFetch = origCF, origWarp }()

	var cfCalls, warpCalls int32
	latestCloudflaredFetch = func(ctx context.Context) latestVersionItem {
		atomic.AddInt32(&cfCalls, 1)
		return latestVersionItem{Latest: "2026.11.0", CheckedAt: nowMillis(), Source: "github-release"}
	}
	latestWarpFetch = func(ctx context.Context) latestVersionItem {
		atomic.AddInt32(&warpCalls, 1)
		return latestVersionItem{Latest: "2026.8.2115.0", CheckedAt: nowMillis(), Source: "apt-cloudflare"}
	}
	h := &handlers{log: testLogger()}

	call := func(query string) map[string]any {
		rec := httptest.NewRecorder()
		h.latestVersions(rec, httptest.NewRequest(http.MethodGet, "/api/versions/latest"+query, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d：%s", rec.Code, rec.Body.String())
		}
		var out struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("解析响应失败：%v", err)
		}
		if !out.Success {
			t.Fatalf("success 应为 true：%s", rec.Body.String())
		}
		return out.Data
	}

	data := call("")
	cf, _ := data["cloudflared"].(map[string]any)
	warp, _ := data["warp-svc"].(map[string]any)
	if cf == nil || warp == nil {
		t.Fatalf("data 缺少 cloudflared / warp-svc：%v", data)
	}
	if cf["latest"] != "2026.11.0" || cf["source"] != "github-release" || cf["error"] != "" {
		t.Fatalf("cloudflared 字段 = %v", cf)
	}
	if warp["latest"] != "2026.8.2115.0" || warp["source"] != "apt-cloudflare" {
		t.Fatalf("warp-svc 字段 = %v", warp)
	}
	if _, ok := cf["checked_at"].(float64); !ok {
		t.Fatalf("checked_at 应为毫秒数字：%v", cf["checked_at"])
	}

	// 第二次：命中缓存，不再调用数据源
	_ = call("")
	if cfCalls != 1 || warpCalls != 1 {
		t.Fatalf("缓存未命中：cf=%d warp=%d", cfCalls, warpCalls)
	}

	// ?refresh=1：强制刷新
	_ = call("?refresh=1")
	if cfCalls != 2 || warpCalls != 2 {
		t.Fatalf("refresh=1 未刷新：cf=%d warp=%d", cfCalls, warpCalls)
	}
	_ = call("?refresh=true")
	if cfCalls != 3 {
		t.Fatalf("refresh=true 未刷新：cf=%d", cfCalls)
	}
}

func TestLatestVersionsEndpointSingleFailureDegrades(t *testing.T) {
	resetLatestVersionsCache()
	defer resetLatestVersionsCache()
	origCF, origWarp := latestCloudflaredFetch, latestWarpFetch
	defer func() { latestCloudflaredFetch, latestWarpFetch = origCF, origWarp }()

	latestCloudflaredFetch = func(ctx context.Context) latestVersionItem {
		return latestVersionItem{CheckedAt: nowMillis(), Error: "GitHub API 返回 HTTP 500"}
	}
	latestWarpFetch = func(ctx context.Context) latestVersionItem {
		return latestVersionItem{Latest: "2026.8.2115.0", CheckedAt: nowMillis(), Source: "apt-cloudflare"}
	}
	h := &handlers{log: testLogger()}
	rec := httptest.NewRecorder()
	h.latestVersions(rec, httptest.NewRequest(http.MethodGet, "/api/versions/latest?refresh=1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("单项失败 HTTP 仍应 200，得到 %d", rec.Code)
	}
	var out struct {
		Data map[string]map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	cf := out.Data["cloudflared"]
	if cf["latest"] != "" || cf["error"] == "" {
		t.Fatalf("失败项应 latest 为空 + error 非空：%v", cf)
	}
	if out.Data["warp-svc"]["latest"] != "2026.8.2115.0" {
		t.Fatalf("另一项不应受影响：%v", out.Data["warp-svc"])
	}
}

// 并发请求只触发一次刷新（缓存锁串行化），且无数据竞争。
func TestGetLatestVersionsConcurrentSingleFlight(t *testing.T) {
	resetLatestVersionsCache()
	defer resetLatestVersionsCache()
	origCF, origWarp := latestCloudflaredFetch, latestWarpFetch
	defer func() { latestCloudflaredFetch, latestWarpFetch = origCF, origWarp }()

	var calls int32
	latestCloudflaredFetch = func(ctx context.Context) latestVersionItem {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		return latestVersionItem{Latest: "2026.11.0", CheckedAt: nowMillis(), Source: "github-release"}
	}
	latestWarpFetch = func(ctx context.Context) latestVersionItem {
		return latestVersionItem{Latest: "2026.8.2115.0", CheckedAt: nowMillis(), Source: "apt-cloudflare"}
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := getLatestVersions(false)
			if data.Cloudflared.Latest != "2026.11.0" {
				t.Errorf("cloudflared = %+v", data.Cloudflared)
			}
		}()
	}
	wg.Wait()
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("并发冷启动应只刷新一次，实际 %d 次", n)
	}
}
