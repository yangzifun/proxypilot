package api

import (
	"strings"
	"testing"
)

// 回归（任务清理 bug）：ack 回执里 failed/skipped 终态原来不写 finished_at，
// 导致面板「任务」永久展示 + 保留期清理永远删不掉（线上已积累 600+ 条 7 天前任务）。
func TestAgentTaskFinishedAt(t *testing.T) {
	const now = int64(1794487000000)
	cases := []struct {
		status string
		want   int64
	}{
		{"pending", 0}, // 还要重试，不能写结束时间
		{"sent", 0},
		{"", 0},
		{"done", now},
		{"failed", now},
		{"skipped", now},
		{"expired", now},
	}
	for _, c := range cases {
		if got := agentTaskFinishedAt(c.status, now); got != c.want {
			t.Fatalf("agentTaskFinishedAt(%q)=%d，期望 %d", c.status, got, c.want)
		}
	}
}

// 清理 SQL 必须同时覆盖「有 finished_at 的正常终态」和「历史 finished_at=0 的终态」。
func TestAgentTasksCleanupSQLHandlesLegacyZeroFinishedAt(t *testing.T) {
	for _, want := range []string{
		"finished_at > 0 AND finished_at < ?",
		"finished_at = 0 OR finished_at IS NULL",
		"created_at > 0 AND created_at < ?",
	} {
		if !strings.Contains(agentTasksCleanupSQL, want) {
			t.Fatalf("清理 SQL 缺少片段 %q：%s", want, agentTasksCleanupSQL)
		}
	}
}
