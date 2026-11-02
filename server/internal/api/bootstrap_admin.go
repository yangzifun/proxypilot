package api

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// 0001_init.sql 里的默认口令（password）哈希，用于识别“仍是默认密码”的部署。
const defaultAdminSHA256 = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8"

// EnsureAdminPassword 生产启动兜底：
//   - 设置 PP_ADMIN_PASSWORD 且 admin 仍是默认口令 → 改为环境变量口令；
//   - 未设置且仍是默认口令 → 打印高危告警（不阻断启动）。
func EnsureAdminPassword(ctx context.Context, db *sql.DB, cfg config.Config, log *slog.Logger) {
	if db == nil {
		return
	}
	var hash sql.NullString
	if err := db.QueryRowContext(ctx,
		`SELECT password_hash FROM admin_users WHERE username = ? LIMIT 1`, cfg.SuperAdmin).Scan(&hash); err != nil {
		if err != sql.ErrNoRows {
			log.Warn("检查管理员口令失败", "err", err)
		}
		return
	}
	isDefault := strings.EqualFold(strings.TrimSpace(hash.String), defaultAdminSHA256)
	if !isDefault {
		return
	}
	if pw := strings.TrimSpace(cfg.AdminPassword); pw != "" {
		newHash, err := auth.HashPassword(pw)
		if err != nil {
			log.Error("设置初始管理员口令失败", "err", err)
			return
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE admin_users SET password_hash = ? WHERE username = ?`, newHash, cfg.SuperAdmin); err != nil {
			log.Error("写入初始管理员口令失败", "err", err)
			return
		}
		log.Info("已使用 PP_ADMIN_PASSWORD 初始化管理员口令", "user", cfg.SuperAdmin)
		return
	}
	log.Warn("管理员仍在使用默认口令 password：请立即登录后台修改，或设置 PP_ADMIN_PASSWORD 后重启",
		"user", cfg.SuperAdmin)
}
