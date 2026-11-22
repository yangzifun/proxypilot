package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestAdminSuperMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0061_admin_super.sql")
	if err != nil {
		t.Fatalf("读取 0061 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"ALTER TABLE `admin_users`",
		"`is_super` TINYINT NOT NULL DEFAULT 0",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0061 迁移缺少 %q", want)
		}
	}
	stmts := db.SplitStatements(text)
	if len(stmts) != 1 {
		t.Fatalf("0061 应只有 1 条语句，实际 %d", len(stmts))
	}
	if strings.HasSuffix(stmts[0], ";") {
		t.Fatal("迁移语句不应带末尾分号")
	}
}