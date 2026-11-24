package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DoH（DNS over HTTPS）默认配置。
//
// 面板只提供 /dns-query/<uuid> 入口：不递归、不开放 53、不部署到节点。
// 上游固定使用 HTTPS POST raw DNS wire（RFC8484），默认 Cloudflare + Google。
const (
	defaultDoHDomain       = "proxypilot.yangzifun.org"
	defaultDoHMaxQPS       = 20
	defaultDoHMaxBodyBytes = 4096
	dohMaxUpstreamBody     = 65535
	dohUpstreamTimeout     = 5 * time.Second
	dohMaxUpstreams        = 8
)

var defaultDoHUpstreams = []string{
	"https://cloudflare-dns.com/dns-query",
	"https://dns.google/dns-query",
}

// dohUpstreamClient 独立于面板其他 HTTP 客户端：严格的 5s 超时，测试可覆盖。
var dohUpstreamClient = &http.Client{
	Timeout: dohUpstreamTimeout,
	// 上游只允许 HTTPS；即使上游返回 30x 也不允许降级到明文 HTTP。
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("DoH 上游重定向过多")
		}
		if !strings.EqualFold(req.URL.Scheme, "https") {
			return errors.New("DoH 上游不允许降级到非 HTTPS")
		}
		return nil
	},
}

// dohRuntimeConfig 是某次请求生效的 DoH 配置（app_settings + 默认值）。
type dohRuntimeConfig struct {
	Enabled      bool
	Domain       string
	Upstreams    []string
	MaxQPS       int
	LogQueries   bool
	MaxBodyBytes int
}

// dohURLOf 生成某个 UUID 的 DoH URL。域名只允许 host，不含 scheme/path。
func dohURLOf(domain, uuid string) string {
	return "https://" + domain + "/dns-query/" + uuid
}

// normalizeDoHDomain 校验并归一化 DoH 域名。
// 允许用户误带 https:// 前缀/末尾斜杠，但拒绝路径、端口、查询串和非法字符。
func normalizeDoHDomain(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return "", fmt.Errorf("不能为空")
	}
	if strings.ContainsAny(s, " \t\r\n/?#:@") {
		return "", fmt.Errorf("只能是域名（不含协议、路径、端口）")
	}
	// 纯 hostname：标签允许字母数字与短横线，不能以短横线开头/结尾。
	if len(s) > 253 {
		return "", fmt.Errorf("域名过长")
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("域名格式无效")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("域名格式无效")
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return "", fmt.Errorf("域名含非法字符")
			}
		}
	}
	return s, nil
}

// parseDoHUpstreams 解析逗号分隔或 JSON 数组形式的上游 URL 列表。
// 只允许 HTTPS；禁止 userinfo/fragment/query；路径为空时补 /dns-query。
func parseDoHUpstreams(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return append([]string(nil), defaultDoHUpstreams...), nil
	}
	var candidates []string
	if strings.HasPrefix(raw, "[") {
		if err := json.Unmarshal([]byte(raw), &candidates); err != nil {
			return nil, fmt.Errorf("必须是逗号分隔 URL 或 JSON 字符串数组")
		}
	} else {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r' || r == ' '
		}) {
			if part = strings.TrimSpace(part); part != "" {
				candidates = append(candidates, part)
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("至少需要一个 HTTPS 上游")
	}
	if len(candidates) > dohMaxUpstreams {
		return nil, fmt.Errorf("上游最多 %d 个", dohMaxUpstreams)
	}
	out := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, c := range candidates {
		u, err := url.Parse(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("URL 无效")
		}
		if !strings.EqualFold(u.Scheme, "https") {
			return nil, fmt.Errorf("只允许 HTTPS 上游")
		}
		if u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
			return nil, fmt.Errorf("上游必须是 HTTPS URL（禁止账号密码/query/fragment）")
		}
		if u.Path == "" {
			u.Path = "/dns-query"
		}
		normalized := u.String()
		if !seen[normalized] {
			seen[normalized] = true
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("至少需要一个 HTTPS 上游")
	}
	return out, nil
}

func dohIntSetting(v string, fallback, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < lo || n > hi {
		return fallback
	}
	return n
}

// dohRuntime 读取 DoH 当前生效配置；配置损坏时回退安全默认值。
func (h *handlers) dohRuntime(ctx context.Context) dohRuntimeConfig {
	enabled := h.settingBool(ctx, settingDoHEnabled, true)
	domain, err := normalizeDoHDomain(h.setting(ctx, settingDoHDomain, defaultDoHDomain))
	if err != nil || domain == "" {
		domain = defaultDoHDomain
	}
	upstreams, err := parseDoHUpstreams(h.setting(ctx, settingDoHUpstreams, strings.Join(defaultDoHUpstreams, ",")))
	if err != nil || len(upstreams) == 0 {
		upstreams = append([]string(nil), defaultDoHUpstreams...)
	}
	maxQPS := dohIntSetting(h.setting(ctx, settingDoHMaxQPS, "20"), defaultDoHMaxQPS, 1, 10000)
	maxBody := dohIntSetting(h.setting(ctx, settingDoHMaxBodyBytes, "4096"), defaultDoHMaxBodyBytes, 512, 65535)
	return dohRuntimeConfig{
		Enabled: enabled, Domain: domain, Upstreams: upstreams,
		MaxQPS: maxQPS, LogQueries: h.settingBool(ctx, settingDoHLogQueries, false),
		MaxBodyBytes: maxBody,
	}
}

// ---------------- UUID / 限速 ----------------

// dohUUIDStatus 查询 UUID 状态：只有 uuid_info.status=active 才放行。
// 无记录（未注册/已删除）一律拒绝，避免任意随机 UUID 滥用 DoH。
func (h *handlers) dohUUIDStatus(ctx context.Context, uuid string) (string, bool, error) {
	var status sql.NullString
	err := h.db.QueryRowContext(ctx,
		`SELECT status FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "missing", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s := strings.ToLower(strings.TrimSpace(status.String))
	if s != "active" {
		return s, false, nil
	}
	return s, true, nil
}

type dohRateBucket struct {
	second int64
	count  int
}

type dohRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*dohRateBucket
}

func newDoHRateLimiter() *dohRateLimiter {
	return &dohRateLimiter{buckets: map[string]*dohRateBucket{}}
}

var dohRate = newDoHRateLimiter()

// allow 实现按 key（uuid|ip）的秒级固定窗口限速。
// limit<=0 时使用默认 20；返回是否放行。
func (l *dohRateLimiter) allow(key string, limit int, now time.Time) bool {
	if limit <= 0 {
		limit = defaultDoHMaxQPS
	}
	sec := now.Unix()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*dohRateBucket{}
	}
	b := l.buckets[key]
	if b == nil || b.second != sec {
		b = &dohRateBucket{second: sec}
		l.buckets[key] = b
		// 简单清理：map 过大时丢掉非当前秒的旧桶，避免长期运行内存增长。
		if len(l.buckets) > 10000 {
			for k, old := range l.buckets {
				if old.second != sec {
					delete(l.buckets, k)
				}
			}
		}
	}
	if b.count >= limit {
		return false
	}
	b.count++
	return true
}

func resetDoHRateLimiter() {
	dohRate.mu.Lock()
	dohRate.buckets = map[string]*dohRateBucket{}
	dohRate.mu.Unlock()
}

func dohRateKey(uuid, ip string) string {
	if ip == "" {
		ip = "-"
	}
	return uuid + "|" + ip
}

// ---------------- 请求解析 / 上游 ----------------

var errDoHTooLarge = errors.New("DoH 请求体过大")

// readDoHPayload 解析 GET ?dns=base64url 或 POST raw DNS wire，并限制大小。
func readDoHPayload(w http.ResponseWriter, r *http.Request, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = defaultDoHMaxBodyBytes
	}
	switch r.Method {
	case http.MethodGet:
		raw := strings.TrimSpace(r.URL.Query().Get("dns"))
		if raw == "" {
			return nil, errors.New("缺少 dns 参数")
		}
		b, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			b, err = base64.URLEncoding.DecodeString(raw)
		}
		if err != nil || len(b) == 0 {
			return nil, errors.New("dns 参数不是合法的 base64url")
		}
		if len(b) > maxBytes {
			return nil, errDoHTooLarge
		}
		return b, nil
	case http.MethodPost:
		ct := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
		if ct != "application/dns-message" {
			return nil, errors.New("Content-Type 必须是 application/dns-message")
		}
		reader := http.MaxBytesReader(w, r.Body, int64(maxBytes))
		b, err := io.ReadAll(reader)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return nil, errDoHTooLarge
			}
			return nil, errors.New("读取请求体失败")
		}
		if len(b) == 0 {
			return nil, errors.New("请求体为空")
		}
		if len(b) > maxBytes {
			return nil, errDoHTooLarge
		}
		return b, nil
	default:
		return nil, errors.New("只支持 GET/POST")
	}
}

// dohQuestionName 从 DNS wire 中提取 QNAME（仅 doh_log_queries=1 时用于审计日志）。
// 默认配置不调用、不记录；解析失败返回空串，绝不记录原始查询内容。
func dohQuestionName(payload []byte) string {
	if len(payload) < 13 {
		return ""
	}
	labels := make([]string, 0, 4)
	off := 12
	for off < len(payload) {
		n := int(payload[off])
		off++
		if n == 0 {
			break
		}
		if n > 63 || off+n > len(payload) {
			return ""
		}
		labels = append(labels, string(payload[off:off+n]))
		off += n
		if len(labels) > 16 {
			return ""
		}
	}
	return strings.Join(labels, ".")
}

// dohForward 依次尝试上游，返回第一个 200 的非空 raw DNS 响应。
func (h *handlers) dohForward(ctx context.Context, upstreams []string, payload []byte) ([]byte, error) {
	if len(upstreams) == 0 {
		return nil, errors.New("没有可用的 DoH 上游")
	}
	var lastErr error
	for _, upstream := range upstreams {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream, bytes.NewReader(payload))
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/dns-message")
		req.Header.Set("Accept", "application/dns-message")
		req.Header.Set("User-Agent", "ProxyPilot-DoH/1.0")
		resp, err := dohUpstreamClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, dohMaxUpstreamBody+1))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("上游 %s 返回 HTTP %d", upstream, resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			lastErr = fmt.Errorf("上游 %s 返回空响应", upstream)
			continue
		}
		if len(body) > dohMaxUpstreamBody {
			lastErr = fmt.Errorf("上游 %s 响应过大", upstream)
			continue
		}
		return body, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的 DoH 上游")
	}
	return nil, lastErr
}

// recordDoHUsage 按 UUID+小时 upsert 统计；不记录查询内容。
func (h *handlers) recordDoHUsage(ctx context.Context, uuid string, bytesIn, bytesOut int64) {
	if h.db == nil || uuid == "" {
		return
	}
	now := nowMillis()
	hour := now - now%3_600_000
	if _, err := h.db.ExecContext(ctx, `INSERT INTO doh_usage_hourly
	      (uuid, node_id, hour_ts, queries, bytes_in, bytes_out, updated_at)
	      VALUES (?, 0, ?, 1, ?, ?, ?)
	      ON DUPLICATE KEY UPDATE queries = queries + 1,
	      bytes_in = bytes_in + VALUES(bytes_in),
	      bytes_out = bytes_out + VALUES(bytes_out),
	      updated_at = VALUES(updated_at)`,
		uuid, hour, bytesIn, bytesOut, now); err != nil {
		if h.log != nil {
			h.log.Warn("写入 DoH 统计失败", "uuid", uuid, "err", err)
		}
	}
}

// ---------------- 公开入口 ----------------

// dohQuery 处理 GET/POST /dns-query/{uuid}。
// 不做管理员鉴权，只按 UUID 状态鉴权；响应/日志都不包含 DNS 查询内容。
func (h *handlers) dohQuery(w http.ResponseWriter, r *http.Request) {
	cfg := h.dohRuntime(r.Context())
	if !cfg.Enabled {
		writeError(w, http.StatusForbidden, "DoH 未启用")
		return
	}
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !regexpUUID.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式无效")
		return
	}
	// UUID 大小写不影响归属；统一小写查 uuid_info，避免大写路径绕过停用状态。
	uuid = strings.ToLower(uuid)
	ctx := r.Context()
	if status, ok, err := h.dohUUIDStatus(ctx, uuid); err != nil {
		writeError(w, http.StatusInternalServerError, "查询 UUID 状态失败")
		return
	} else if !ok {
		if status == "missing" || status == "" {
			writeError(w, http.StatusForbidden, "UUID 不存在或未授权")
		} else {
			writeError(w, http.StatusForbidden, "UUID 已停用")
		}
		return
	}
	if !dohRate.allow(dohRateKey(uuid, clientIP(r)), cfg.MaxQPS, time.Now()) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "DoH 请求过于频繁")
		return
	}
	payload, err := readDoHPayload(w, r, cfg.MaxBodyBytes)
	if err != nil {
		if errors.Is(err, errDoHTooLarge) {
			writeError(w, http.StatusBadRequest, "请求体超过限制")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if cfg.LogQueries && h.log != nil {
		// 只有显式开启才记录 QNAME；默认完全不落查询内容。
		if qname := dohQuestionName(payload); qname != "" {
			h.log.Info("DoH 查询", "uuid", uuid, "qname", qname)
		}
	}
	upCtx, cancel := context.WithTimeout(ctx, dohUpstreamTimeout)
	defer cancel()
	body, err := h.dohForward(upCtx, cfg.Upstreams, payload)
	if err != nil {
		// 上游超时/取消时请求 ctx 可能已经结束；失败统计用独立短上下文，避免丢计数。
		recordCtx, recordCancel := context.WithTimeout(context.Background(), 2*time.Second)
		h.recordDoHUsage(recordCtx, uuid, int64(len(payload)), 0)
		recordCancel()
		if h.log != nil {
			h.log.Warn("DoH 上游失败", "uuid", uuid, "err", err)
		}
		writeError(w, http.StatusBadGateway, "DoH 上游暂不可用")
		return
	}
	h.recordDoHUsage(ctx, uuid, int64(len(payload)), int64(len(body)))
	w.Header().Set("Content-Type", "application/dns-message")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
