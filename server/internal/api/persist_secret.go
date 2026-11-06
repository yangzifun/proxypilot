package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log/slog"
	"strings"
)

// EnsurePersistentSecret 保证签名/加密密钥稳定：优先读取数据库中的持久值，
// 其次使用环境变量（首次运行时写库），最后才生成随机值。
//
// 这样即使服务端重启、容器重建或 .env 丢失/重新生成，
// 已签发的登录 token 与 Agent 令牌仍然有效，不需要全体重新登录。
func EnsurePersistentSecret(ctx context.Context, db *sql.DB, key, envValue string, minLen int, log *slog.Logger) string {
	if db == nil {
		return strings.TrimSpace(envValue)
	}
	var stored string
	err := db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE `key` = ? LIMIT 1", key).Scan(&stored)
	if err == nil && strings.TrimSpace(stored) != "" {
		if log != nil {
			log.Info("使用数据库持久化密钥", "key", key)
		}
		return strings.TrimSpace(stored)
	}
	value := strings.TrimSpace(envValue)
	if len(value) < minLen {
		buf := make([]byte, 48)
		if _, rerr := rand.Read(buf); rerr != nil {
			return value
		}
		value = hex.EncodeToString(buf)
		if log != nil {
			log.Warn("未配置或密钥过短，已生成随机密钥并写入数据库", "key", key)
		}
	}
	if _, ierr := db.ExecContext(ctx,
		"INSERT INTO app_settings (`key`, value, updated_at) VALUES (?, ?, UNIX_TIMESTAMP()*1000) "+
			"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		key, value); ierr == nil && log != nil {
		log.Info("已写入数据库持久化密钥", "key", key)
	}
	return value
}
