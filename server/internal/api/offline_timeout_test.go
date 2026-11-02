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
	if got := agentOnlineStatus(now-59_000, "online", 10); got != "online" {
		t.Fatalf("59 秒无心跳不应立即离线，得到 %q", got)
	}
	if got := agentOnlineStatus(now-61_000, "online", 10); got != "offline" {
		t.Fatalf("超过离线阈值应离线，得到 %q", got)
	}
}
