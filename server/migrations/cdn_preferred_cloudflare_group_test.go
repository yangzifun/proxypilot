package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// 0056：CDN 优选默认分组 default → cloudflare（task-18）。
// 这里只做「文件已内嵌 + SQL 可拆分 + 幂等语义」的静态校验；
// 真正的数据改名在部署时由迁移执行器执行（幂等：重复执行影响 0 行）。
func TestCDNPreferredCloudflareGroupMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0056_cdn_preferred_cloudflare_group.sql")
	if err != nil {
		t.Fatalf("读取 0056 迁移失败: %v", err)
	}
	src := string(body)
	stmts := db.SplitStatements(src)
	if len(stmts) != 1 {
		t.Fatalf("应拆出 1 条语句，实际 %d 条", len(stmts))
	}
	stmt := stmts[0]
	if !strings.HasPrefix(stmt, "UPDATE `cdn_preferred_addresses`") {
		t.Fatalf("语句应是更新 cdn_preferred_addresses 的 UPDATE：%q", stmt)
	}
	if !strings.Contains(stmt, "`group_name` = 'cloudflare'") {
		t.Fatalf("缺少把 group_name 改为 cloudflare 的赋值：%q", stmt)
	}
	// WHERE 必须只命中 default（大小写/空格容忍），保证重复执行幂等、不误改自定义分组
	if !strings.Contains(stmt, "LOWER(TRIM(`group_name`)) = 'default'") {
		t.Fatalf("WHERE 条件应只匹配 default（幂等且不误伤自定义分组）：%q", stmt)
	}
	// 迁移只做改名，不删数据、不动 address/port 等业务字段
	if strings.Contains(strings.ToUpper(stmt), "DELETE") {
		t.Fatal("改名迁移不应删除数据")
	}
	for _, field := range []string{"`address`", "`port`", "`enabled`"} {
		if strings.Contains(stmt, field+" =") {
			t.Fatalf("改名迁移不应改写 %s：%q", field, stmt)
		}
	}
	if !strings.Contains(src, "幂等") {
		t.Fatal("迁移注释应说明幂等（可安全重复执行）")
	}
}
