package api

import (
	"net/http/httptest"
	"testing"
)

// 限流器的语义是「预约即计数」：Blocked() 在口令校验前先占用一次尝试额度，
// 因此这里是连续调用 Blocked 直到它返回 blocked，而不是调用 Fail（Fail 是空实现，
// 仅为兼容旧调用点保留）。这样做的目的是消除并发请求的 check-then-act 窗口。
func TestLoginLimiterBlocksAfterFailures(t *testing.T) {
	l := &loginRateLimiter{m: make(map[string]*loginAttempt)}
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	key := loginRateKey(r, "admin")

	if _, blocked := l.Blocked(key); blocked {
		t.Fatal("第一次尝试不应被锁定")
	}

	// 第 1 次已在上面用掉，这里再消耗 loginMaxFailures-1 次，累计达到阈值
	var wait int64
	var blocked bool
	for i := 1; i < loginMaxFailures; i++ {
		if w, b := l.Blocked(key); b {
			wait, blocked = int64(w), b
			break
		}
	}
	if !blocked {
		t.Fatalf("连续 %d 次尝试后应被锁定", loginMaxFailures)
	}
	if wait <= 0 {
		t.Fatalf("锁定时应返回正的等待时长，实际 %d", wait)
	}

	// 锁定期间继续请求仍然是拒绝
	if _, b := l.Blocked(key); !b {
		t.Fatal("锁定期内应继续拒绝")
	}

	l.Reset(key)
	if _, blocked := l.Blocked(key); blocked {
		t.Fatal("Reset 后应立即解锁")
	}
}

// Fail 现在是空实现（额度在 Blocked 里预约），这里锁住这个契约，
// 避免以后有人误以为 Fail 在记账。
func TestLoginLimiterFailIsNoop(t *testing.T) {
	l := &loginRateLimiter{m: make(map[string]*loginAttempt)}
	key := "1.2.3.4|admin"
	for i := 0; i < loginMaxFailures*3; i++ {
		l.Fail(key)
	}
	if _, blocked := l.Blocked(key); blocked {
		t.Fatal("Fail 不应影响计数，第一次 Blocked 不该被锁定")
	}
}
