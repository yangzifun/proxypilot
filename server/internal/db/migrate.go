package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
  version VARCHAR(191) NOT NULL,
  applied_at BIGINT NOT NULL,
  PRIMARY KEY (version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

// Migrate 按文件名顺序执行 fsys 中尚未应用的 *.sql，并返回本次应用的版本列表。
// 注意：MySQL 的 DDL 会隐式提交，因此这里不包事务，改为“逐条执行 + 成功后记录版本”。
func Migrate(ctx context.Context, pool *sql.DB, fsys fs.FS) ([]string, error) {
	if _, err := pool.ExecContext(ctx, createMigrationsTable); err != nil {
		return nil, fmt.Errorf("创建 schema_migrations 失败: %w", err)
	}

	applied := map[string]bool{}
	rows, err := pool.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("读取 schema_migrations 失败: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	var done []string
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return done, fmt.Errorf("读取迁移 %s 失败: %w", name, err)
		}
		stmts := SplitStatements(string(body))
		for i, stmt := range stmts {
			if _, err := pool.ExecContext(ctx, stmt); err != nil {
				return done, fmt.Errorf("迁移 %s 第 %d/%d 条语句失败: %w\nSQL: %s",
					name, i+1, len(stmts), err, truncate(stmt, 300))
			}
		}
		if _, err := pool.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
			version, time.Now().UnixMilli()); err != nil {
			return done, fmt.Errorf("记录迁移 %s 失败: %w", name, err)
		}
		done = append(done, version)
	}
	return done, nil
}

// SplitStatements 按分号拆分 SQL，忽略字符串/反引号/注释里的分号。
func SplitStatements(src string) []string {
	var out []string
	var b strings.Builder
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == '-' && i+1 < n && src[i+1] == '-':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		case c == '\'' || c == '"' || c == '`':
			quote := c
			b.WriteByte(c)
			i++
			for i < n {
				ch := src[i]
				b.WriteByte(ch)
				i++
				if ch == '\\' && quote != '`' && i < n {
					b.WriteByte(src[i])
					i++
					continue
				}
				if ch == quote {
					// 连续两个引号表示转义
					if i < n && src[i] == quote {
						b.WriteByte(src[i])
						i++
						continue
					}
					break
				}
			}
		case c == ';':
			if stmt := strings.TrimSpace(b.String()); stmt != "" {
				out = append(out, stmt)
			}
			b.Reset()
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	if stmt := strings.TrimSpace(b.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
