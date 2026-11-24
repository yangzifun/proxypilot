package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestCertStatusMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0064_singbox_cert_status.sql")
	if err != nil {
		t.Fatalf("读取 0064 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"ALTER TABLE `servers`",
		"`cert_fingerprint` TEXT",
		"`cert_synced_at` BIGINT",
		"`cert_status` VARCHAR(32)",
		"`cert_error` TEXT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0064 迁移缺少 %q", want)
		}
	}
	if len(db.SplitStatements(text)) != 1 {
		t.Fatalf("0064 应为单条 ALTER 语句")
	}
}