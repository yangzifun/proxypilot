package api

import (
	"net/http/httptest"
	"testing"
)

func TestLoginLimiterBlocksAfterFailures(t *testing.T) {
	l := &loginRateLimiter{m: make(map[string]*loginAttempt)}
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	key := loginRateKey(r, "admin")

	if _, blocked := l.Blocked(key); blocked {
		t.Fatal("初始不应被锁定")
	}
	for i := 0; i < loginMaxFailures; i++ {
		l.Fail(key)
	}
	wait, blocked := l.Blocked(key)
	if !blocked || wait <= 0 {
		t.Fatalf("连续失败 %d 次后应被锁定，wait=%v blocked=%v", loginMaxFailures, wait, blocked)
	}
	l.Reset(key)
	if _, blocked := l.Blocked(key); blocked {
		t.Fatal("Reset 后应立即解锁")
	}
}
