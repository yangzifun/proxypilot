package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// 0055：清理 servers.last_ip6 里的 WARP 段、last_ip4 里的 CGNAT 组网地址（task-15）。
// 这里只做「文件已内嵌 + SQL 可拆分 + 覆盖两个 WARP 段」的静态校验；
// 真正的数据清理在部署时由迁移执行器执行（幂等）。
func TestClearWarpLastIPMigration(t *testing.T) {
	body, err := migrations.FS.ReadFile("0055_clear_warp_last_ip.sql")
	if err != nil {
		t.Fatalf("读取 0055 迁移失败: %v", err)
	}
	src := string(body)
	stmts := db.SplitStatements(src)
	if len(stmts) != 2 {
		t.Fatalf("应拆出 2 条语句（last_ip6 / last_ip4），实际 %d 条", len(stmts))
	}
	if !strings.HasPrefix(stmts[0], "UPDATE servers") || !strings.Contains(stmts[0], "last_ip6 = ''") {
		t.Fatalf("第一条语句应是清理 last_ip6 的 UPDATE: %q", stmts[0])
	}
	if !strings.HasPrefix(stmts[1], "UPDATE servers") || !strings.Contains(stmts[1], "last_ip4 = ''") {
		t.Fatalf("第二条语句应是清理 last_ip4 的 UPDATE: %q", stmts[1])
	}
	// 只清 Cloudflare WARP 主段 2606:4700::/32；2602:fa4f::/32 是东京2/东京3
	// 物理网卡上的真实公网 IPv6（2026-11-12 现场核实），迁移不得清理。
	if !strings.Contains(stmts[0], "2606:4700::") {
		t.Fatal("迁移缺少 WARP 段 2606:4700:: 的清理条件")
	}
	for _, stmt := range stmts {
		if strings.Contains(stmt, "2602:fa4f:") {
			t.Fatal("2602:fa4f::/32 是真实公网 IPv6，迁移语句不得把它当 WARP 清理")
		}
	}
	if !strings.Contains(src, "INET6_ATON") {
		t.Fatal("last_ip6 清理应使用 INET6_ATON 做段匹配（避免字符串前缀误伤）")
	}
	if !strings.Contains(src, "last_ip4 REGEXP") {
		t.Fatal("last_ip4 清理应匹配 CGNAT 100.64/10")
	}
}
