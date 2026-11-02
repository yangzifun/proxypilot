package migrations_test

import (
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

func TestInitSQLIsEmbeddedAndSplittable(t *testing.T) {
	body, err := migrations.FS.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatalf("读取内嵌 SQL 失败: %v", err)
	}
	stmts := db.SplitStatements(string(body))
	if len(stmts) < 60 {
		t.Fatalf("语句数 = %d，疑似拆分或文件缺失", len(stmts))
	}
	creates, alters := 0, 0
	for _, s := range stmts {
		if strings.HasPrefix(s, "CREATE TABLE") {
			creates++
		}
		if strings.HasPrefix(s, "ALTER TABLE") {
			alters++
		}
		if strings.HasSuffix(s, ";") {
			t.Fatalf("语句不应带尾分号: %q", s)
		}
	}
	if creates != 26 {
		t.Fatalf("CREATE TABLE 数量 = %d，期望 26", creates)
	}
	if alters == 0 {
		t.Fatal("没有 ALTER TABLE（索引/生成列缺失）")
	}
	if !strings.Contains(string(body), "utf8mb4_0900_as_cs") {
		t.Fatal("缺少大小写敏感排序规则")
	}
	// 空邮箱必须落成 NULL，否则第二个“不填邮箱”的分组会撞唯一索引
	if !strings.Contains(string(body), "NULLIF(LOWER(`email`), '')") {
		t.Fatal("email_norm 生成列缺少 NULLIF(...,'')")
	}
}
