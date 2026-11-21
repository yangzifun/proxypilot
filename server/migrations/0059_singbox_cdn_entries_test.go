package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// 0059：sing-box 节点多 CDN 条目。
// 这里只做「文件已内嵌 + SQL 可拆分 + 幂等回填语义」的静态校验；
// 实际 DDL/回填在部署时由迁移器执行（ALTER 按 schema_migrations 只跑一次，
// UPDATE 带空条件可重复执行）。
func TestSingboxCDNEntriesMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0059_singbox_cdn_entries.sql")
	if err != nil {
		t.Fatalf("读取 0059 迁移失败: %v", err)
	}
	src := string(body)
	stmts := db.SplitStatements(src)
	if len(stmts) != 2 {
		t.Fatalf("应拆出 2 条语句（ALTER + 回填 UPDATE），实际 %d 条", len(stmts))
	}

	alter := stmts[0]
	if !strings.HasPrefix(alter, "ALTER TABLE `singbox_nodes`") {
		t.Fatalf("第一条语句应对 singbox_nodes 执行 ALTER，实际：%q", alter)
	}
	if !strings.Contains(alter, "ADD COLUMN `cdn_entries` LONGTEXT") {
		t.Fatalf("缺少 cdn_entries LONGTEXT 列：%q", alter)
	}

	update := stmts[1]
	if !strings.HasPrefix(update, "UPDATE `singbox_nodes`") {
		t.Fatalf("第二条语句应是回填 UPDATE，实际：%q", update)
	}
	for _, want := range []string{
		"JSON_ARRAY(JSON_OBJECT(",
		"'domain', LOWER(TRIM(`cdn_domain`))",
		"'primary', TRUE",
		"'prefer_group', ''",
		"TRIM(`cdn_domain`) <> ''",
	} {
		if !strings.Contains(update, want) {
			t.Fatalf("回填 SQL 缺少 %q：%q", want, update)
		}
	}
	// 幂等：只回填 cdn_entries 为空的旧行，重复执行影响 0 行。
	if !strings.Contains(update, "`cdn_entries` IS NULL OR TRIM(`cdn_entries`) IN ('', '[]', 'null')") {
		t.Fatalf("回填缺少幂等条件：%q", update)
	}
	// 迁移不得删除业务数据，也不得改动旧列语义。
	if strings.Contains(strings.ToUpper(src), "DELETE") || strings.Contains(strings.ToUpper(src), "DROP ") {
		t.Fatal("迁移不应删除数据或列")
	}
	if strings.Contains(src, "`cdn_domain` =") {
		t.Fatal("迁移不应改写旧列 cdn_domain")
	}
	if !strings.Contains(src, "幂等") {
		t.Fatal("迁移注释应说明幂等（可安全重复执行）")
	}
}
