package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestNotificationLogsMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0065_notification_logs.sql")
	if err != nil {
		t.Fatalf("读取 0065 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `notification_logs`",
		"`kind` VARCHAR(32)",
		"`title` VARCHAR(191)",
		"`server_ids` TEXT",
		"`recipients` TEXT",
		"`status` VARCHAR(16)",
		"`error` TEXT",
		"`created_at` BIGINT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0065 迁移缺少 %q", want)
		}
	}
	if len(db.SplitStatements(text)) != 1 {
		t.Fatal("0065 应为单条 CREATE 语句")
	}
}

func TestServerStatusEventsMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0066_server_status_events.sql")
	if err != nil {
		t.Fatalf("读取 0066 迁移失败：%v", err)
	}
	text := string(body)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS `server_status_events`",
		"`server_id` BIGINT",
		"`event` VARCHAR(16)",
		"`reason` TEXT",
		"`occurred_at` BIGINT",
		"`duration_sec` BIGINT",
		"`created_at` BIGINT",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("0066 迁移缺少 %q", want)
		}
	}
	if len(db.SplitStatements(text)) != 1 {
		t.Fatal("0066 应为单条 CREATE 语句")
	}
}