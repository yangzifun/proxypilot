package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const subUserInfo = "upload=0; download=0; total=10737418240000; expire=2524608000"

var proxySchemes = []string{
	"vmess://", "vless://", "trojan://", "ss://", "ssr://",
	"hysteria://", "hysteria2://", "hy2://", "tuic://", "anytls://",
}

// 外部订阅源缓存：Worker 用 Cache API 缓存 120s；Go 侧用进程内缓存，
// 多副本时各自缓存一份，效果略弱但能挡住绝大多数重复回源。
const extCacheTTL = 120 * time.Second

type extCacheEntry struct {
	links       []string
	unsupported int
	at          time.Time
}

var (
	extCacheMu sync.Mutex
	extCache   = map[string]extCacheEntry{}
)

type extSummary struct {
	Sources     int
	Added       int
	Errors      int
	Unsupported int
}

// GET /sub/{uuid}
func (h *handlers) rawSubscription(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "UUID Required")
		return
	}
	if !h.subscribeAllowed(w, r, uuid) {
		return
	}
	lines, summary, err := h.subscriptionLines(r.Context(), uuid)
	if err != nil {
		h.log.Error("生成订阅失败", "uuid", uuid, "err", err)
		writeError(w, http.StatusInternalServerError, "生成订阅失败")
		return
	}
	if len(lines) == 0 {
		w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("UUID Not Found or Empty（本地配置与外部订阅源均为空）"))
		return
	}
	h.logAccess(r, uuid, "subscription")

	w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	w.Header().Set("Subscription-User-Info", subUserInfo)
	setExternalHeaders(w, summary)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))))
}

// GET /batch-configs/{uuid}
func (h *handlers) batchSubscription(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "UUID Required")
		return
	}
	if !h.subscribeAllowed(w, r, uuid) {
		return
	}
	lines, summary, err := h.subscriptionLines(r.Context(), uuid)
	if err != nil {
		h.log.Error("生成订阅失败", "uuid", uuid, "err", err)
		writeError(w, http.StatusInternalServerError, "生成订阅失败")
		return
	}
	if len(lines) == 0 {
		writeError(w, http.StatusNotFound, "UUID Not Found or Empty（本地配置与外部订阅源均为空）")
		return
	}
	h.logAccess(r, uuid, "subscription")

	w.Header().Set("Content-Type", "text/plain;charset=UTF-8")
	w.Header().Set("Subscription-User-Info", subUserInfo)
	w.Header().Set("Profile-Update-Interval", "24")
	setExternalHeaders(w, summary)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(lines, "\n")))))
}

func setExternalHeaders(w http.ResponseWriter, s extSummary) {
	w.Header().Set("X-External-Sources", strconv.Itoa(s.Sources))
	w.Header().Set("X-External-Nodes", strconv.Itoa(s.Added))
	w.Header().Set("X-External-Errors", strconv.Itoa(s.Errors))
	w.Header().Set("X-External-Unsupported", strconv.Itoa(s.Unsupported))
}

// subscribeAllowed 处理停用/暂停状态；不通过时已写出响应并返回 false。
func (h *handlers) subscribeAllowed(w http.ResponseWriter, r *http.Request, uuid string) bool {
	var status sql.NullString
	err := h.db.QueryRowContext(r.Context(),
		`SELECT status FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).Scan(&status)
	if err != nil {
		return true // 无元信息 = 视为 active（与 Worker 一致）
	}
	if status.Valid && (status.String == "inactive" || status.String == "suspended") {
		label := "已停用"
		if status.String == "suspended" {
			label = "已暂停"
		}
		writeError(w, http.StatusForbidden, "该 UUID "+label+"，无法获取订阅")
		return false
	}
	return true
}

func (h *handlers) subscriptionLines(ctx context.Context, uuid string) ([]string, extSummary, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT config_data FROM configs WHERE uuid = ? ORDER BY id ASC`, uuid)
	if err != nil {
		return nil, extSummary{}, err
	}
	defer rows.Close()

	seen := map[string]bool{}
	var lines []string
	for rows.Next() {
		var raw sql.NullString
		if err := rows.Scan(&raw); err != nil {
			return nil, extSummary{}, err
		}
		if !raw.Valid || strings.TrimSpace(raw.String) == "" {
			continue
		}
		if seen[raw.String] {
			continue
		}
		seen[raw.String] = true
		lines = append(lines, raw.String)
	}
	if err := rows.Err(); err != nil {
		return nil, extSummary{}, err
	}

	lines = expandStoredCDNLinks(lines)
	lines = h.filterSuspendedLines(ctx, lines, uuid)

	ext, summary := h.externalLinks(ctx, uuid)
	for _, line := range ext {
		if seen[line] {
			continue
		}
		seen[line] = true
		lines = append(lines, line)
		summary.Added++
	}
	return lines, summary, nil
}

// externalLinks 拉取该 UUID 绑定的外部订阅源（带 120s 缓存）。
func (h *handlers) externalLinks(ctx context.Context, uuid string) ([]string, extSummary) {
	var summary extSummary
	rows, err := h.db.QueryContext(ctx,
		`SELECT url FROM uuid_sub_sources WHERE uuid = ? AND enabled = 1 ORDER BY id ASC LIMIT 10`, uuid)
	if err != nil {
		return nil, summary // 表不存在或查询失败时忽略外部源
	}
	var urls []string
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil && strings.TrimSpace(u) != "" {
			urls = append(urls, strings.TrimSpace(u))
		}
	}
	rows.Close()
	summary.Sources = len(urls)

	client := subSourceHTTPClient()
	var links []string
	for _, u := range urls {
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			summary.Errors++
			continue
		}
		cached, ok := cacheGet(u)
		if ok {
			links = append(links, cached.links...)
			summary.Unsupported += cached.unsupported
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			summary.Errors++
			continue
		}
		req.Header.Set("User-Agent", "ProxyPilot/1.0")
		resp, err := client.Do(req)
		if err != nil {
			h.log.Warn("拉取外部订阅失败", "url", u, "err", err)
			summary.Errors++
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if err != nil || resp.StatusCode >= 400 {
			summary.Errors++
			continue
		}
		parsed, unsupported := parseSubscriptionBody(string(body))
		cachePut(u, parsed, unsupported)
		links = append(links, parsed...)
		summary.Unsupported += unsupported
		if len(links) > 500 {
			links = links[:500]
			break
		}
	}
	return links, summary
}

func cacheGet(url string) (extCacheEntry, bool) {
	extCacheMu.Lock()
	defer extCacheMu.Unlock()
	e, ok := extCache[url]
	if !ok || time.Since(e.at) > extCacheTTL {
		return extCacheEntry{}, false
	}
	return e, true
}

func cachePut(url string, links []string, unsupported int) {
	extCacheMu.Lock()
	defer extCacheMu.Unlock()
	if len(extCache) > 500 {
		extCache = map[string]extCacheEntry{}
	}
	extCache[url] = extCacheEntry{links: links, unsupported: unsupported, at: time.Now()}
}

// parseSubscriptionBody 兼容 base64 与明文两种订阅响应，返回代理链接与“无法识别的行”数量。
func parseSubscriptionBody(body string) ([]string, int) {
	text := strings.TrimSpace(body)
	if !strings.Contains(text, "://") {
		compact := strings.Join(strings.Fields(text), "")
		if dec, err := base64.StdEncoding.DecodeString(compact); err == nil {
			text = string(dec)
		} else if dec, err := base64.RawStdEncoding.DecodeString(compact); err == nil {
			text = string(dec)
		}
	}
	var (
		out         []string
		unsupported int
	)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if isProxyLink(line) {
			out = append(out, line)
			continue
		}
		if strings.Contains(line, "://") {
			unsupported++
		}
	}
	return out, unsupported
}

func isProxyLink(line string) bool {
	for _, s := range proxySchemes {
		if strings.HasPrefix(line, s) {
			return true
		}
	}
	return false
}

// logAccess 写访问日志并累加统计（失败不影响订阅输出）。
func (h *handlers) logAccess(r *http.Request, uuid, queryType string) {
	ip := clientIP(r)
	if ip == "" {
		ip = "unknown"
	}
	ua := r.Header.Get("User-Agent")
	if ua == "" {
		ua = "unknown"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO config_access_logs (uuid, query_type, client_ip, user_agent, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		uuid, queryType, ip, ua, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	if err == nil {
		_, err = h.db.ExecContext(ctx,
			`UPDATE uuid_info SET last_accessed = ?, access_count = COALESCE(access_count,0) + 1 WHERE uuid = ?`,
			(nowMillis()/1000)*1000, uuid)
	}
	if err != nil && !errorsIsNoRows(err) {
		h.log.Warn("记录订阅访问失败", "uuid", uuid, "err", err)
	}
}

func errorsIsNoRows(err error) bool { return err == sql.ErrNoRows }
