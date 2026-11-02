package api

import "testing"

func TestAgentOnlineStatusNoHeartbeat(t *testing.T) {
	if got := agentOnlineStatus(0, "online", 60); got != "offline" {
		t.Fatalf("无心跳应离线，得到 %q", got)
	}
	if got := agentOnlineStatus(nowMillis(), "online", 60); got != "online" {
		t.Fatalf("刚心跳应在线，得到 %q", got)
	}
	old := nowMillis() - 10*60*1000
	if got := agentOnlineStatus(old, "online", 60); got != "offline" {
		t.Fatalf("心跳超时应离线，得到 %q", got)
	}
}
