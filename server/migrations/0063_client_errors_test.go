package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestClientErrorsMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0063_client_errors.sql")
	if err != nil {
		t.Fatalf("读取 0063 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `client_errors`",
		"`message` VARCHAR(500)",
		"`stack` TEXT",
		"`url` VARCHAR(500)",
		"`page` VARCHAR(500)",
		"`mode` VARCHAR(20)",
		"`ua` VARCHAR(300)",
		"`created_at` BIGINT",
		"KEY `idx_client_errors_created` (`created_at`)",
		"KEY `idx_client_errors_mode_created` (`mode`, `created_at`)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0063 迁移缺少 %q", want)
		}
	}
	stmts := db.SplitStatements(text)
	if len(stmts) != 1 {
		t.Fatalf("0063 应为 1 条 CREATE TABLE 语句，实际 %d", len(stmts))
	}
}
