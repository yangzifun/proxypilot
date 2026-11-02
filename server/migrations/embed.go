// Package migrations 以 embed 方式携带 SQL 迁移文件，二进制可独立部署。
package migrations

import "embed"

// FS 包含 migrations 目录下的全部 .sql（按文件名顺序执行）。
//
//go:embed *.sql
var FS embed.FS
