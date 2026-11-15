package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// =====================================================================
//  组件最新版本查询（cloudflared / warp-svc）
//
//  数据源（面板实测可用）：
//   - cloudflared：GitHub /releases/latest 的 302 Location（tag），
//     失败再退回 GitHub API releases/latest 的 tag_name；
//   - warp-svc：Cloudflare 官方 apt 源 Packages 文件里 cloudflare-warp 的 Version，
//     按本机 /etc/os-release 的 VERSION_CODENAME 取 dists 目录；
//     失败时兜底尝试常见 codename（resolute/noble/jammy/focal），取最大版本。
//
//  性能与稳定性：内存缓存 30 分钟，?refresh=1 强制刷新；每个数据源 10s 超时；
//  单项失败不影响另一项，HTTP 仍返回 200（该项 latest 为空 + error 带原因）。
//  所有网络请求/系统文件都通过包级变量注入，便于单测离线覆盖解析与降级路径。
// =====================================================================

const (
	// latestVersionsTTL 最新版本缓存有效期。
	latestVersionsTTL = 30 * time.Minute
	// latestVersionsTimeout 一次刷新（两个数据源共享）的总超时。
	latestVersionsTimeout = 10 * time.Second
	// latestVersionsUserAgent 请求 GitHub/Cloudflare 时使用的 UA（避免被默认 UA 拦截）。
	latestVersionsUserAgent = "ProxyPilot-Panel/latest-version-check"

	cloudflaredReleasesLatestURL = "https://github.com/cloudflare/cloudflared/releases/latest"
	cloudflaredGitHubAPIURL      = "https://api.github.com/repos/cloudflare/cloudflared/releases/latest"
	warpPackagesURLTemplate      = "https://pkg.cloudflareclient.com/dists/%s/main/binary-amd64/Packages"
	warpPackageName              = "cloudflare-warp"
)

// latestWarpCodenameFallbacks WARP apt 源兜底 codename（按 Ubuntu 由新到旧）。
var latestWarpCodenameFallbacks = []string{"resolute", "noble", "jammy", "focal"}

// latestVersionItem 单个组件的「最新版本」查询结果（前端契约）。
type latestVersionItem struct {
	Latest    string `json:"latest"`
	CheckedAt int64  `json:"checked_at"`
	Source    string `json:"source"`
	Error     string `json:"error"`
}

// latestVersionsData GET /api/versions/latest 的 data 对象。
type latestVersionsData struct {
	Cloudflared latestVersionItem `json:"cloudflared"`
	WarpSvc     latestVersionItem `json:"warp-svc"`
}

// latestVersionsCache 进程内缓存（面板单实例，内存态足够）。
var latestVersionsCache = struct {
	sync.Mutex
	at   time.Time
	data latestVersionsData
}{}

// latestHTTPClient 用于 GitHub / Cloudflare：10s 超时、不自动跟随重定向
// （cloudflared 最新版本正是从 302 的 Location 里取 tag）。
var latestHTTPClient = &http.Client{
	Timeout: latestVersionsTimeout,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// latestHTTPDo 网络请求注入点（单测替换，不联网也能覆盖解析/降级/缓存）。
var latestHTTPDo = func(req *http.Request) (*http.Response, error) { return latestHTTPClient.Do(req) }

// latestCloudflaredFetch / latestWarpFetch 数据源函数注入点（单测替换，便于测缓存与降级）。
var (
	latestCloudflaredFetch = fetchCloudflaredLatest
	latestWarpFetch        = fetchWarpLatest
)

// latestOSReleaseFile 读取系统 codename 的文件（单测可替换成临时文件）。
var latestOSReleaseFile = "/etc/os-release"

// latestHTTPGet 发起一次带 UA 的 GET 请求。
func latestHTTPGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", latestVersionsUserAgent)
	req.Header.Set("Accept", "application/vnd.github+json, text/plain;q=0.9, */*;q=0.5")
	return latestHTTPDo(req)
}

// shortLatestErr 错误信息截断，避免把大段底层错误塞进响应。
func shortLatestErr(err error) string {
	if err == nil {
		return ""
	}
	return truncateRunes(err.Error(), 160)
}

// trimVersionPrefix 去掉 tag/版本号里的前导 v（只处理 v+数字，避免误伤）。
func trimVersionPrefix(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 1 && (v[0] == 'v' || v[0] == 'V') && v[1] >= '0' && v[1] <= '9' {
		return v[1:]
	}
	return v
}

// parseCloudflaredTagLocation 从 GitHub releases/latest 的 Location 里解析版本号。
// 形如 https://github.com/cloudflare/cloudflared/releases/tag/2026.11.0
func parseCloudflaredTagLocation(location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", errors.New("Location 为空")
	}
	u, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("Location 不是合法 URL：%v", err)
	}
	path := strings.TrimSuffix(u.Path, "/")
	const marker = "/cloudflare/cloudflared/releases/tag/"
	idx := strings.Index(path, marker)
	if idx < 0 {
		return "", fmt.Errorf("Location 不是 releases/tag 链接：%s", location)
	}
	tag := trimVersionPrefix(path[idx+len(marker):])
	if tag == "" {
		return "", errors.New("Location 缺少 tag")
	}
	return tag, nil
}

// parseWarpPackagesVersion 解析 Debian 的 Packages 文件，取指定包的最大 Version。
// 字段按行解析（Package/Version 不会跨行续行）；多个同名 stanza 取数字段最大者。
func parseWarpPackagesVersion(body, pkg string) (string, error) {
	if strings.TrimSpace(body) == "" {
		return "", errors.New("Packages 内容为空")
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	best := ""
	checkStanza := func(lines []string) {
		name, version := "", ""
		for _, line := range lines {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "Package:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "Package:"))
			case strings.HasPrefix(line, "Version:"):
				version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
			}
		}
		if name != pkg || version == "" {
			return
		}
		if best == "" || agentVersionNewer(version, best) {
			best = version
		}
	}
	lines := make([]string, 0, 16)
	for _, raw := range strings.Split(body, "\n") {
		if strings.TrimSpace(raw) == "" {
			checkStanza(lines)
			lines = lines[:0]
			continue
		}
		lines = append(lines, raw)
	}
	checkStanza(lines)
	if best == "" {
		return "", fmt.Errorf("Packages 中未找到 %s 的 Version", pkg)
	}
	return best, nil
}

// osVersionCodename 读取 /etc/os-release 的系统代号（优先 VERSION_CODENAME）。
func osVersionCodename(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	versionCodename, ubuntuCodename := "", ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.ToLower(strings.Trim(strings.TrimSpace(val), `"'`))
		switch strings.TrimSpace(key) {
		case "VERSION_CODENAME":
			versionCodename = val
		case "UBUNTU_CODENAME":
			ubuntuCodename = val
		}
	}
	if versionCodename != "" {
		return versionCodename
	}
	return ubuntuCodename
}

// fetchCloudflaredLatest 查 cloudflared 最新版本：
//  1. GitHub /releases/latest 的 302 Location（tag）；
//  2. 失败退回 GitHub API 的 tag_name。
func fetchCloudflaredLatest(ctx context.Context) latestVersionItem {
	item := latestVersionItem{CheckedAt: nowMillis()}
	var errs []string

	if resp, err := latestHTTPGet(ctx, cloudflaredReleasesLatestURL); err != nil {
		errs = append(errs, "GitHub releases/latest 请求失败："+shortLatestErr(err))
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			if version, perr := parseCloudflaredTagLocation(resp.Header.Get("Location")); perr == nil {
				item.Latest, item.Source = version, "github-release"
				return item
			} else {
				errs = append(errs, "GitHub 重定向解析失败："+shortLatestErr(perr))
			}
		default:
			errs = append(errs, fmt.Sprintf("GitHub releases/latest 返回 HTTP %d（预期 302）", resp.StatusCode))
		}
	}

	if resp, err := latestHTTPGet(ctx, cloudflaredGitHubAPIURL); err != nil {
		errs = append(errs, "GitHub API 请求失败："+shortLatestErr(err))
	} else {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var payload struct {
				TagName string `json:"tag_name"`
			}
			if json.Unmarshal(body, &payload) == nil {
				if version := trimVersionPrefix(payload.TagName); version != "" {
					item.Latest, item.Source = version, "github-api"
					return item
				}
			}
			errs = append(errs, "GitHub API 响应缺少 tag_name")
		} else {
			errs = append(errs, fmt.Sprintf("GitHub API 返回 HTTP %d", resp.StatusCode))
		}
	}

	item.Error = strings.Join(errs, "；")
	return item
}

// fetchWarpPackagesVersion 从某个 codename 的 apt Packages 文件解析 cloudflare-warp 版本。
func fetchWarpPackagesVersion(ctx context.Context, codename string) (string, error) {
	rawURL := fmt.Sprintf(warpPackagesURLTemplate, url.PathEscape(codename))
	resp, err := latestHTTPGet(ctx, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	return parseWarpPackagesVersion(string(body), warpPackageName)
}

// fetchWarpLatest 查 WARP 客户端（cloudflare-warp 包）最新版本：
// 先按本机 codename 查官方 apt 源；失败时兜底常见 codename，取能解析到的最大版本。
func fetchWarpLatest(ctx context.Context) latestVersionItem {
	item := latestVersionItem{CheckedAt: nowMillis()}
	primary := osVersionCodename(latestOSReleaseFile)
	errs := make([]string, 0, len(latestWarpCodenameFallbacks)+1)

	if primary != "" {
		version, err := fetchWarpPackagesVersion(ctx, primary)
		if err == nil {
			item.Latest, item.Source = version, "apt-cloudflare"
			return item
		}
		errs = append(errs, primary+"："+shortLatestErr(err))
	}

	// 兜底：遍历常见 codename，取能解析到的最大版本。
	versions := make([]string, 0, len(latestWarpCodenameFallbacks))
	for _, codename := range latestWarpCodenameFallbacks {
		if codename == primary {
			continue
		}
		version, err := fetchWarpPackagesVersion(ctx, codename)
		if err != nil {
			errs = append(errs, codename+"："+shortLatestErr(err))
			continue
		}
		versions = append(versions, version)
	}
	if len(versions) > 0 {
		best := versions[0]
		for _, v := range versions[1:] {
			if agentVersionNewer(v, best) {
				best = v
			}
		}
		item.Latest, item.Source = best, "apt-cloudflare"
		return item
	}

	if len(errs) == 0 {
		errs = append(errs, "未识别系统 codename，且兜底 codename 均未取到版本")
	}
	item.Error = strings.Join(errs, "；")
	return item
}

// getLatestVersions 返回（可能命中缓存的）最新版本数据；refresh=true 强制刷新。
//
// 缓存锁在整个刷新期间持有：并发请求最多等一次刷新（≤10s），不会重复打
// GitHub/Cloudflare；随后统一拿到同一份结果。
func getLatestVersions(refresh bool) latestVersionsData {
	latestVersionsCache.Lock()
	defer latestVersionsCache.Unlock()
	if !refresh && !latestVersionsCache.at.IsZero() &&
		time.Since(latestVersionsCache.at) < latestVersionsTTL {
		return latestVersionsCache.data
	}

	ctx, cancel := context.WithTimeout(context.Background(), latestVersionsTimeout)
	defer cancel()

	var data latestVersionsData
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		data.Cloudflared = latestCloudflaredFetch(ctx)
	}()
	go func() {
		defer wg.Done()
		data.WarpSvc = latestWarpFetch(ctx)
	}()
	wg.Wait()

	latestVersionsCache.at = time.Now()
	latestVersionsCache.data = data
	return data
}

// GET /api/versions/latest?refresh=1 —— cloudflared / warp-svc 最新可用版本。
//
// 单项失败不影响整体：该项 latest 为空、error 带原因，HTTP 仍 200。
func (h *handlers) latestVersions(w http.ResponseWriter, r *http.Request) {
	refresh := false
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("refresh"))) {
	case "1", "true", "yes":
		refresh = true
	}
	data := getLatestVersions(refresh)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}
