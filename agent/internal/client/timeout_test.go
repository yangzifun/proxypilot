package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// P1-4 回归：poll / ack 必须有单次请求超时。
//
// 面板或中间设备「接受 TCP 但不回响应」时，没有 deadline 的请求会一直挂着，
// Once() 不返回 → 心跳、任务、ack 全部停摆，而进程还活着（systemd 不会重启）。
func TestPollAndAckHaveRequestTimeout(t *testing.T) {
	// 服务端只收请求、不回响应（模拟挂起的 Worker / 半开连接）。
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release) // LIFO：先放行 handler，再关 http 服务

	prevPoll, prevAck := pollTimeout, ackTimeout
	pollTimeout = 120 * time.Millisecond
	ackTimeout = 80 * time.Millisecond
	defer func() { pollTimeout, ackTimeout = prevPoll, prevAck }()

	c := New(srv.URL, "ag_"+strings.Repeat("a", 64), "test", false)
	cases := []struct {
		name    string
		call    func(context.Context) error
		maximum time.Duration
	}{
		{
			name: "poll 超时",
			call: func(ctx context.Context) error {
				_, err := c.Poll(ctx, PollRequest{})
				return err
			},
			maximum: pollTimeout + 2*time.Second,
		},
		{
			name: "ack 超时",
			call: func(ctx context.Context) error {
				return c.Ack(ctx, AckRequest{AgentVersion: "test"})
			},
			maximum: ackTimeout + 2*time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			err := tc.call(context.Background())
			elapsed := time.Since(start)
			if err == nil {
				t.Fatal("响应挂起时必须超时返回错误")
			}
			if elapsed > tc.maximum {
				t.Fatalf("超时未生效：耗时 %s，上限 %s", elapsed, tc.maximum)
			}
			if !strings.Contains(err.Error(), "deadline exceeded") {
				t.Fatalf("错误应说明超时：%v", err)
			}
		})
	}
}

// 默认值必须有限：poll 可以长，但要有限；ack 比 poll 短（ack 会重试 3 次）。
func TestDefaultRequestTimeoutsAreFinite(t *testing.T) {
	if pollTimeout <= 0 || pollTimeout > 5*time.Minute {
		t.Fatalf("poll 默认超时不合理：%s", pollTimeout)
	}
	if ackTimeout <= 0 || ackTimeout > 5*time.Minute {
		t.Fatalf("ack 默认超时不合理：%s", ackTimeout)
	}
	if ackTimeout >= pollTimeout {
		t.Fatalf("ack 应比 poll 短：ack=%s poll=%s", ackTimeout, pollTimeout)
	}
	if pollTimeout != PollTimeout || ackTimeout != AckTimeout {
		t.Fatalf("生产默认值必须等于常量：poll=%s/%s ack=%s/%s", pollTimeout, PollTimeout, ackTimeout, AckTimeout)
	}
}
