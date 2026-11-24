package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	clientErrorMaxBody   = 16 << 10 // 16KB
	clientErrorMessage   = 500
	clientErrorStack     = 4000
	clientErrorURL       = 500
	clientErrorPage      = 500
	clientErrorUA        = 300
	clientErrorAt        = 64
	clientErrorMode      = 20
	clientErrorPerMinute = 30
)

type clientErrorPayload struct {
	Message string          `json:"message"`
	Stack   string          `json:"stack"`
	URL     string          `json:"url"`
	Page    string          `json:"page"`
	Mode    string          `json:"mode"`
	UA      string          `json:"ua"`
	At      json.RawMessage `json:"at"`
}

type clientErrorBucket struct {
	minute int64
	count  int
}

// clientErrorLimiter IP 级固定一分钟窗口限速；不写数据库，仅进程内存。
type clientErrorLimiter struct {
	mu      sync.Mutex
	buckets map[string]*clientErrorBucket
}

func newClientErrorLimiter() *clientErrorLimiter {
	return &clientErrorLimiter{buckets: map[string]*clientErrorBucket{}}
}

func (l *clientErrorLimiter) allow(ip string, limit int, now time.Time) bool {
	if ip == "" {
		ip = "unknown"
	}
	minute := now.Unix() / 60
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 4096 {
		for k, b := range l.buckets {
			if b.minute != minute {
				delete(l.buckets, k)
			}
		}
	}
	b := l.buckets[ip]
	if b == nil || b.minute != minute {
		b = &clientErrorBucket{minute: minute}
		l.buckets[ip] = b
	}
	if b.count >= limit {
		return false
	}
	b.count++
	return true
}

var (
	clientErrorRate      = newClientErrorLimiter()
	clientErrorRateLimit = clientErrorPerMinute
)

// sanitizeClientErrorURL 去掉 query/fragment，避免把 URL 中的 token/敏感参数写进日志。
func sanitizeClientErrorURL(raw string) string {
	s := truncateRunes(strings.TrimSpace(raw), clientErrorURL)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.IsAbs() {
		u.RawQuery = ""
		u.Fragment = ""
		return truncateRunes(u.String(), clientErrorURL)
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	return s
}

func clientErrorAtText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	return truncateRunes(strings.TrimSpace(string(raw)), clientErrorAt)
}

func clientErrorModeText(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "admin", "portal":
		return mode
	case "":
		return "unknown"
	default:
		return truncateRunes(mode, clientErrorMode)
	}
}

// POST /api/client-errors
// 公开客户端错误上报：仅写 slog Warn；限速 + 字段截断防止日志放大器。
func (h *handlers) clientErrorReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}

	ip := clientIP(r)
	if !clientErrorRate.allow(ip, clientErrorRateLimit, time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, "上报过于频繁，请稍后再试")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, clientErrorMaxBody+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "读取请求失败")
		return
	}
	if len(body) > clientErrorMaxBody {
		writeError(w, http.StatusRequestEntityTooLarge, "请求体超过 16KB")
		return
	}
	var p clientErrorPayload
	if len(body) == 0 || json.Unmarshal(body, &p) != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}

	if h.log != nil {
		h.log.Warn("客户端错误上报",
			"message", truncateRunes(strings.TrimSpace(p.Message), clientErrorMessage),
			"stack", truncateRunes(strings.TrimSpace(p.Stack), clientErrorStack),
			"url", sanitizeClientErrorURL(p.URL),
			"page", truncateRunes(strings.TrimSpace(p.Page), clientErrorPage),
			"mode", clientErrorModeText(p.Mode),
			"ua", truncateRunes(strings.TrimSpace(p.UA), clientErrorUA),
			"at", clientErrorAtText(p.At),
		)
	}

	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
