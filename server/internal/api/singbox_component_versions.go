package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// componentVersionFile 是桶索引（versions.json / manifest.json）中单个架构的文件描述。
type componentVersionFile struct {
	Binary    string `json:"binary,omitempty"`
	Tar       string `json:"tar,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
	TarSHA256 string `json:"tar_sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
	TarSize   int64  `json:"tar_size,omitempty"`
}

// componentVersionEntry 是返回给前端的桶内 sing-box 版本项。
type componentVersionEntry struct {
	Version    string                          `json:"version"`
	Prerelease bool                            `json:"prerelease"`
	ReleasedAt string                          `json:"released_at,omitempty"`
	Default    bool                            `json:"default"`
	Source     string                          `json:"source"`
	Files      map[string]componentVersionFile `json:"files,omitempty"`
}

// componentVersionIndex 是桶版本索引文件的顶层结构；兼容纯数组。
type componentVersionIndex struct {
	Default  string                 `json:"default,omitempty"`
	Versions []componentVersionItem `json:"versions,omitempty"`
}

type componentVersionItem struct {
	Version    string                          `json:"version"`
	Tag        string                          `json:"tag,omitempty"`
	Prerelease bool                            `json:"prerelease,omitempty"`
	ReleasedAt string                          `json:"released_at,omitempty"`
	Files      map[string]componentVersionFile `json:"files,omitempty"`
}

// componentVersionManifest 兼容现有 manifest.json 的单版本结构。
type componentVersionManifest struct {
	Version    string                          `json:"version"`
	ReleasedAt string                          `json:"released_at,omitempty"`
	Files      map[string]componentVersionFile `json:"files,omitempty"`
}

var (
	componentVersionsMu       sync.Mutex
	componentVersionsCache    []componentVersionEntry
	componentVersionsCacheAt  time.Time
	componentVersionsCacheSrc string
	componentVersionsCacheWrn string
)

const componentVersionsTTL = 5 * time.Minute

// parseComponentVersionFiles 解析不同形态的 files 映射；只接受 linux-<arch> 键。
func parseComponentVersionFiles(raw map[string]any) map[string]componentVersionFile {
	out := make(map[string]componentVersionFile, len(raw))
	for arch, v := range raw {
		key := strings.TrimSpace(strings.ToLower(arch))
		if !strings.HasPrefix(key, "linux-") {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[key] = componentVersionFile{
			Binary:    strings.TrimSpace(jsonStr(m["binary"])),
			Tar:       strings.TrimSpace(jsonStr(m["tar"])),
			SHA256:    strings.TrimSpace(jsonStr(m["sha256"])),
			TarSHA256: strings.TrimSpace(jsonStr(m["tar_sha256"])),
			Size:      jsonInt64(m["size"]),
			TarSize:   jsonInt64(m["tar_size"]),
		}
	}
	return out
}

func jsonInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		return 0
	}
}

func componentVersionIsPrerelease(v string, explicit bool) bool {
	if explicit {
		return true
	}
	low := strings.ToLower(strings.TrimSpace(v))
	return strings.Contains(low, "alpha") || strings.Contains(low, "beta") || strings.Contains(low, "rc")
}

// parseComponentVersionsJSON 解析 versions.json（对象 {"versions":[...]} 或数组）或 manifest.json。
// 返回条目不依赖网络，便于单测。
func parseComponentVersionsJSON(body []byte, source string) ([]componentVersionEntry, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("空版本索引")
	}
	// 先尝试对象；若顶层是数组则直接解析数组。
	var raw any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, err
	}
	var items []componentVersionItem
	defaultVersion := ""
	switch obj := raw.(type) {
	case []any:
		for _, x := range obj {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, componentVersionItem{
				Version:    firstNonEmptyStr(jsonStr(m["version"]), strings.TrimPrefix(jsonStr(m["tag"]), "v")),
				Prerelease: componentVersionIsPrerelease(jsonStr(m["version"]), jsonBool(m["prerelease"])),
				ReleasedAt: jsonStr(m["released_at"]),
				Files:      parseComponentVersionFiles(asMapAny(m["files"])),
			})
		}
	case map[string]any:
		defaultVersion = strings.TrimSpace(jsonStr(obj["default"]))
		if arr, ok := obj["versions"].([]any); ok && len(arr) > 0 {
			for _, x := range arr {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				items = append(items, componentVersionItem{
					Version:    firstNonEmptyStr(jsonStr(m["version"]), strings.TrimPrefix(jsonStr(m["tag"]), "v")),
					Prerelease: componentVersionIsPrerelease(jsonStr(m["version"]), jsonBool(m["prerelease"])),
					ReleasedAt: jsonStr(m["released_at"]),
					Files:      parseComponentVersionFiles(asMapAny(m["files"])),
				})
			}
		} else if v := strings.TrimSpace(jsonStr(obj["version"])); v != "" {
			// manifest.json 单版本兼容
			items = append(items, componentVersionItem{
				Version:    v,
				Prerelease: componentVersionIsPrerelease(v, jsonBool(obj["prerelease"])),
				ReleasedAt: jsonStr(obj["released_at"]),
				Files:      parseComponentVersionFiles(asMapAny(obj["files"])),
			})
		}
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("版本索引中没有版本")
	}
	out := make([]componentVersionEntry, 0, len(items))
	for _, it := range items {
		v := strings.TrimSpace(strings.TrimPrefix(it.Version, "v"))
		if v == "" {
			continue
		}
		out = append(out, componentVersionEntry{
			Version:    v,
			Prerelease: componentVersionIsPrerelease(v, it.Prerelease),
			ReleasedAt: it.ReleasedAt,
			Default:    defaultVersion != "" && strings.TrimPrefix(defaultVersion, "v") == v,
			Source:     source,
			Files:      it.Files,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("版本索引中没有有效版本")
	}
	// 默认优先版本放最前；其余按版本数值降序（1.15 在 1.14 前），保证下拉顺序稳定。
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Default != out[j].Default {
			return out[i].Default
		}
		return compareVersion(out[i].Version, out[j].Version) > 0
	})
	return out, nil
}

func compareVersion(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(strings.ToLower(a), "v"), ".")
	pb := strings.Split(strings.TrimPrefix(strings.ToLower(b), "v"), ".")
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var ia, ib int
		if i < len(pa) {
			ia = leadingInt(pa[i])
		}
		if i < len(pb) {
			ib = leadingInt(pb[i])
		}
		if ia != ib {
			if ia > ib {
				return 1
			}
			return -1
		}
	}
	return strings.Compare(strings.ToLower(a), strings.ToLower(b))
}

func leadingInt(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func asMapAny(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func jsonBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// loadComponentVersions 从桶 versions.json / manifest.json 读取版本索引；失败回退 component_version。
func (h *handlers) loadComponentVersions(ctx context.Context) ([]componentVersionEntry, string, string) {
	componentVersionsMu.Lock()
	if componentVersionsCache != nil && time.Since(componentVersionsCacheAt) < componentVersionsTTL {
		list, src, warning := componentVersionsCache, componentVersionsCacheSrc, componentVersionsCacheWrn
		componentVersionsMu.Unlock()
		return list, src, warning
	}
	componentVersionsMu.Unlock()

	base := h.componentBaseURL()
	warning := ""
	if base != "" {
		client := &http.Client{Timeout: 8 * time.Second}
		for _, item := range []struct{ name, source string }{
			{"versions.json", "versions.json"},
			{"manifest.json", "manifest.json"},
		} {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+item.name, nil)
			if err != nil {
				continue
			}
			req.Header.Set("User-Agent", "ProxyPilot")
			resp, err := client.Do(req)
			if err != nil {
				warning = "读取存储桶版本索引失败：" + err.Error()
				continue
			}
			body := make([]byte, 0)
			if resp.StatusCode/100 == 2 {
				buf := make([]byte, 0)
				tmp := make([]byte, 4096)
				for {
					n, rerr := resp.Body.Read(tmp)
					if n > 0 {
						buf = append(buf, tmp[:n]...)
						if len(buf) > 1<<20 {
							break
						}
					}
					if rerr != nil {
						break
					}
				}
				body = buf
			}
			resp.Body.Close()
			if len(body) == 0 {
				continue
			}
			if list, err := parseComponentVersionsJSON(body, item.source); err == nil && len(list) > 0 {
				componentVersionsMu.Lock()
				componentVersionsCache, componentVersionsCacheAt = list, time.Now()
				componentVersionsCacheSrc, componentVersionsCacheWrn = item.source, warning
				componentVersionsMu.Unlock()
				return list, item.source, warning
			}
		}
	}
	fallback := strings.TrimSpace(h.setting(ctx, settingComponentVersion, ""))
	if fallback != "" {
		list := []componentVersionEntry{{
			Version: fallback, Prerelease: componentVersionIsPrerelease(fallback, false),
			Default: true, Source: "setting",
		}}
		componentVersionsMu.Lock()
		componentVersionsCache, componentVersionsCacheAt = list, time.Now()
		componentVersionsCacheSrc, componentVersionsCacheWrn = "setting", warning
		componentVersionsMu.Unlock()
		return list, "setting", warning
	}
	return nil, "", "没有可用的版本索引，且未配置 component_version"
}

// GET /api/singbox/component-versions
func (h *handlers) singboxComponentVersions(w http.ResponseWriter, r *http.Request) {
	list, source, warning := h.loadComponentVersions(r.Context())
	base := h.componentBaseURL()
	resp := map[string]any{
		"success": len(list) > 0, "source": source, "bucket": base,
		"data": list, "default": h.componentVersion(r.Context()),
	}
	if warning != "" {
		resp["warning"] = warning
	}
	if len(list) == 0 {
		writeJSON(w, http.StatusBadGateway, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
