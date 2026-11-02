package api

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 单实例内的登录失败限速。线上是单进程 systemd 服务，内存态足够；
// 如果以后多实例部署，应换成 MySQL/Redis 计数。
const (
	loginMaxFailures = 8
	loginFailWindow  = 10 * time.Minute
	loginBlockFor    = 15 * time.Minute
	loginSweepAt     = 4096
)

type loginAttempt struct {
	failures     int
	firstFailure time.Time
	blockedUntil time.Time
}

type loginRateLimiter struct {
	mu sync.Mutex
	m  map[string]*loginAttempt
}

var loginLimiter = &loginRateLimiter{m: make(map[string]*loginAttempt)}

func loginRateKey(r *http.Request, username string) string {
	host := clientIP(r)
	if host == "" {
		host = strings.TrimSpace(r.RemoteAddr)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
	}
	return strings.ToLower(host) + "|" + strings.ToLower(strings.TrimSpace(username))
}

func retryAfterSeconds(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 1 {
		sec = 1
	}
	return strconv.Itoa(sec)
}

// Blocked 返回建议等待时长和是否应拒绝本次尝试。
func (l *loginRateLimiter) Blocked(key string) (time.Duration, bool) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil {
		return 0, false
	}
	if now.Before(a.blockedUntil) {
		return a.blockedUntil.Sub(now), true
	}
	if !a.firstFailure.IsZero() && now.Sub(a.firstFailure) > loginFailWindow {
		delete(l.m, key)
	}
	return 0, false
}

// Fail 记录一次失败；达到阈值后进入锁定窗口。
func (l *loginRateLimiter) Fail(key string) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.m[key]
	if a == nil || (!a.firstFailure.IsZero() && now.Sub(a.firstFailure) > loginFailWindow) {
		a = &loginAttempt{firstFailure: now}
		l.m[key] = a
	}
	a.failures++
	if a.failures >= loginMaxFailures {
		a.blockedUntil = now.Add(loginBlockFor)
		a.failures = 0
		a.firstFailure = now
	}
	if len(l.m) > loginSweepAt {
		for k, v := range l.m {
			if now.After(v.blockedUntil) && (v.firstFailure.IsZero() || now.Sub(v.firstFailure) > loginFailWindow) {
				delete(l.m, k)
			}
		}
	}
}

func (l *loginRateLimiter) Reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}
