package api

import "testing"

func TestAgentOfflineTimeoutGreaterThanHeartbeat(t *testing.T) {
	for _, poll := range []int64{10, 15, 30, 60, 120, 900} {
		timeout := agentOfflineTimeoutMs(poll)
		if timeout <= poll*1000 {
			t.Fatalf("poll=%ds 时离线阈值 %dms 未大于心跳间隔", poll, timeout)
		}
	}
}

func TestAgentOfflineGraceUsesTimeout(t *testing.T) {
	now := nowMillis()
	if got := agentOnlineStatus(now-299_000, "online", 10); got != "online" {
		t.Fatalf("299 秒无心跳不应判离线，得到 %q", got)
	}
	if got := agentOnlineStatus(now-301_000, "online", 10); got != "offline" {
		t.Fatalf("超过 5 分钟离线阈值应离线，得到 %q", got)
	}
	// 短抖动（截图中的 94 秒）必须保持在线，不能触发上下线邮件。
	if got := agentOnlineStatus(now-94_000, "online", 20); got != "online" {
		t.Fatalf("94 秒空档不应判离线，得到 %q", got)
	}
}
