package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestAdminRootMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0062_admin_root.sql")
	if err != nil {
		t.Fatalf("读取 0062 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"ALTER TABLE `admin_users`",
		"`is_root` TINYINT NOT NULL DEFAULT 0",
		"UPDATE `admin_users`",
		"SET `is_root` = 1, `is_super` = 1",
		"WHERE `username` = 'admin'",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0062 迁移缺少 %q", want)
		}
	}
	stmts := db.SplitStatements(text)
	if len(stmts) != 2 {
		t.Fatalf("0062 应为 ALTER + UPDATE 两条语句，实际 %d", len(stmts))
	}
	for _, stmt := range stmts {
		if strings.HasSuffix(stmt, ";") {
			t.Fatal("迁移语句不应带末尾分号")
		}
	}
}