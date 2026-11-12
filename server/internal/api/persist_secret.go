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
// 中-03：数据库中的值可能是 enc:v2:（外部 KEK 加密）或旧版明文；这里负责解密。
// box 为 nil 时退化为旧行为（明文），保证未配置 KEK 的部署仍可启动。
func EnsurePersistentSecret(ctx context.Context, db *sql.DB, key, envValue string, minLen int, box *secretBox, log *slog.Logger) string {
	if db == nil {
		return strings.TrimSpace(envValue)
	}
	var stored string
	err := db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE `key` = ? LIMIT 1", key).Scan(&stored)
	if err == nil && strings.TrimSpace(stored) != "" {
		value := strings.TrimSpace(stored)
		corrupt := false
		if strings.HasPrefix(value, secretAtRestVersion) {
			if plain := box.Open(value); plain != "" {
				value = plain
			} else {
				corrupt = true
				value = ""
			}
		}
		if !corrupt && value != "" {
			if log != nil {
				log.Info("使用数据库持久化密钥", "key", key)
			}
			return value
		}
		if corrupt {
			// KEK 丢失/不匹配：不生成随机值覆盖原密文（会永久丢失密钥）。
			// 显式提供足够长的环境变量密钥时，才用它恢复并重新加密写库。
			env := strings.TrimSpace(envValue)
			if log != nil {
				log.Error("静态加密密钥无法解密（KEK 不匹配或丢失）；未提供环境密钥，不会覆盖库内密文", "key", key)
			}
			if len(env) < minLen {
				return ""
			}
			toStore := env
			if sealed, serr := box.Seal(env); serr == nil {
				toStore = sealed
			}
			if _, ierr := db.ExecContext(ctx,
				"INSERT INTO app_settings (`key`, value, updated_at) VALUES (?, ?, UNIX_TIMESTAMP()*1000) "+
					"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
				key, toStore); ierr == nil && log != nil {
				log.Warn("已使用环境变量密钥恢复并重新加密写库", "key", key)
			}
			return env
		}
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
	toStore := value
	if box.Enabled() {
		if sealed, serr := box.Seal(value); serr == nil {
			toStore = sealed
		} else if log != nil {
			log.Warn("静态加密失败，密钥按明文写入", "key", key, "err", serr)
		}
	}
	if _, ierr := db.ExecContext(ctx,
		"INSERT INTO app_settings (`key`, value, updated_at) VALUES (?, ?, UNIX_TIMESTAMP()*1000) "+
			"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		key, toStore); ierr == nil && log != nil {
		log.Info("已写入数据库持久化密钥", "key", key)
	}
	return value
}
