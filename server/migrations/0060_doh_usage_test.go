package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestDoHUsageMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0060_doh_usage.sql")
	if err != nil {
		t.Fatalf("读取 0060 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `doh_usage_hourly`",
		"`uuid`", "`node_id`", "`hour_ts`", "`queries`", "`bytes_in`", "`bytes_out`", "`updated_at`",
		"PRIMARY KEY (`uuid`, `node_id`, `hour_ts`)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0060 迁移缺少 %q", want)
		}
	}
	stmts := db.SplitStatements(text)
	if len(stmts) != 1 {
		t.Fatalf("0060 应只有 1 条语句，实际 %d", len(stmts))
	}
	if strings.HasSuffix(stmts[0], ";") {
		t.Fatal("迁移语句不应带末尾分号")
	}
}