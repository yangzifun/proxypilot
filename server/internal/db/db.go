// Package db 负责 MySQL 连接池与 schema 迁移。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Open 打开 MySQL 连接池并做一次连通性检查。
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	pool, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 MySQL 失败: %w", err)
	}
	pool.SetMaxOpenConns(20)
	pool.SetMaxIdleConns(20)
	pool.SetConnMaxLifetime(3 * time.Minute)
	pool.SetConnMaxIdleTime(time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.PingContext(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 MySQL 失败: %w", err)
	}
	return pool, nil
}
