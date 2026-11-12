package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// =====================================================================
// 中-03 / 中-06：密钥与私钥「静态加密」，KEK 移到数据库之外
//
// 问题：jwt_secret / agent_key_secret 明文存在 app_settings；其它敏感设置与
// agent_token_enc 的 KEK 又是 agent_key_secret（同库）。这样任何能读数据库/
// 备份的人都能解密或伪造令牌。
//
// 方案：引入面板外部 KEK（PP_SECRET_KEY 环境变量，或 PP_SECRET_KEY_FILE 文件），
// 用它把下列数据加密成 enc:v2: 后落库：
//   - app_settings 里的 jwt_secret / agent_key_secret / 各类 Secret 设置；
//   - servers.agent_token_enc；
//   - vpn_certificates.key_pem（TLS 私钥，迁移 0050 增加 key_pem_enc 列后存这里）。
//
// 兼容性：
//   - 旧 enc:v1: 仍可读（用 agent_key_secret 作为旧 KEK），启动时幂等重加密为 v2；
//   - 解密出来的实际值不变：agent_key_secret 的「值」保持不变，16 台已部署节点的
//     接入令牌哈希不受影响（本改造只改变「库里怎么存」）。
//
// 回滚：设置 PP_SECRET_AT_REST=decrypt 启动一次，会把 v2 解密回旧格式（明文或
// enc:v1:），随后可回滚到旧二进制；详见 fix-security-2-report.md。
// =====================================================================

const (
	secretAtRestVersion = "enc:v2:"
	secretAtRestSalt    = "proxypilot-secret-at-rest-v2"
	secretAtRestInfo    = "app-settings-v2"
)

// secretBox 用外部 KEK 做 AES-256-GCM 加解密。
type secretBox struct {
	key []byte
}

// Enabled 表示是否启用了静态加密（未配置 KEK 且文件无法创建时为 false）。
func (b *secretBox) Enabled() bool { return b != nil && len(b.key) == 32 }

// Seal 返回 "enc:v2:<base64(nonce||ciphertext)>"。
func (b *secretBox) Seal(plain string) (string, error) {
	if !b.Enabled() {
		return "", errors.New("静态加密未启用")
	}
	key := hkdfSHA256(b.key, []byte(secretAtRestSalt), []byte(secretAtRestInfo), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	sealed := gcm.Seal(iv, iv, []byte(plain), nil)
	return secretAtRestVersion + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open 解密 enc:v2:；失败返回空串（调用方按「不可用」处理，不 panic）。
func (b *secretBox) Open(enc string) string {
	if !b.Enabled() {
		return ""
	}
	enc = strings.TrimSpace(enc)
	if !strings.HasPrefix(enc, secretAtRestVersion) {
		return ""
	}
	blob, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(enc, secretAtRestVersion))
	if err != nil || len(blob) < 13 {
		return ""
	}
	key := hkdfSHA256(b.key, []byte(secretAtRestSalt), []byte(secretAtRestInfo), 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(blob) < gcm.NonceSize() {
		return ""
	}
	plain, err := gcm.Open(nil, blob[:gcm.NonceSize()], blob[gcm.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(plain)
}

// LoadSecretBox 加载/生成面板外部 KEK。
//
// 优先级：PP_SECRET_KEY（hex/base64/裸字符串，派生 32 字节）> PP_SECRET_KEY_FILE
// 文件（默认：可执行文件同目录 secret.key，权限 0600，不存在则生成）。
// 任何失败都只告警并返回 nil（退化为旧行为），不阻断启动。
func LoadSecretBox(path string, log *slog.Logger) *secretBox {
	if env := strings.TrimSpace(os.Getenv("PP_SECRET_KEY")); env != "" {
		if key, ok := decodeKEK(env); ok {
			if log != nil {
				log.Info("静态加密已启用（KEK 来自 PP_SECRET_KEY）")
			}
			return &secretBox{key: key}
		}
		if log != nil {
			log.Warn("PP_SECRET_KEY 格式无效（需 ≥32 字节 hex/base64/字符串），静态加密未启用")
		}
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		exe, err := os.Executable()
		if err != nil {
			if log != nil {
				log.Warn("无法确定可执行文件路径，静态加密未启用", "err", err)
			}
			return nil
		}
		path = filepath.Join(filepath.Dir(exe), "secret.key")
	}
	if raw, err := os.ReadFile(path); err == nil {
		if key, ok := decodeKEK(strings.TrimSpace(string(raw))); ok {
			if log != nil {
				log.Info("静态加密已启用（KEK 来自文件）", "path", path)
			}
			return &secretBox{key: key}
		}
		if log != nil {
			log.Error("KEK 文件内容无效，静态加密未启用；请检查文件或删除后重新生成", "path", path)
		}
		return nil
	}
	// 文件不存在：生成 32 字节随机 KEK（0600）。
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		if log != nil {
			log.Warn("生成 KEK 失败，静态加密未启用", "err", err)
		}
		return nil
	}
	value := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		if log != nil {
			log.Warn("创建 KEK 目录失败，静态加密未启用", "path", path, "err", err)
		}
		return nil
	}
	if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
		if log != nil {
			log.Warn("写入 KEK 文件失败，静态加密未启用", "path", path, "err", err)
		}
		return nil
	}
	if log != nil {
		log.Warn("已生成新的静态加密 KEK（请与 .env 一起备份，丢失后库内密钥无法解密）", "path", path)
	}
	// 关键：必须用落盘的同一个值派生 KEK。
	// 之前这里直接返回 raw buf，而下次启动走 decodeKEK(raw hex) → sha256(bytes)，
	// 两条路径得到的密钥不同 —— 生成时加密的数据在重启后全部解不开（实测踩到）。
	derived, ok := decodeKEK(value)
	if !ok {
		if log != nil {
			log.Error("生成的 KEK 无法解析，静态加密未启用", "path", path)
		}
		return nil
	}
	return &secretBox{key: derived}
}

// decodeKEK 支持 hex / base64(std|raw) / 原始字符串，统一派生为 32 字节密钥。
func decodeKEK(raw string) ([]byte, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	if len(raw) >= 64 {
		if b, err := hex.DecodeString(raw); err == nil && len(b) >= 32 {
			sum := sha256.Sum256(b)
			return sum[:], true
		}
	}
	if b, err := base64.StdEncoding.DecodeString(raw); err == nil && len(b) >= 32 {
		sum := sha256.Sum256(b)
		return sum[:], true
	}
	if b, err := base64.RawStdEncoding.DecodeString(raw); err == nil && len(b) >= 32 {
		sum := sha256.Sum256(b)
		return sum[:], true
	}
	if len(raw) >= 32 {
		sum := sha256.Sum256([]byte(raw))
		return sum[:], true
	}
	return nil, false
}

// decodeAtRestValue 读取一个可能被加密的配置/令牌值：
//   - enc:v2: → 外部 KEK 解密；
//   - enc:v1: → 旧 KEK（agent_key_secret）解密；
//   - 其它 → 视为明文。
func (h *handlers) decodeAtRestValue(stored string) string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return ""
	}
	if strings.HasPrefix(stored, secretAtRestVersion) {
		if h.box != nil {
			return h.box.Open(stored)
		}
		return ""
	}
	if strings.HasPrefix(stored, agentKeyVersion) {
		return decryptAgentToken(h.agentKeySecret(), stored)
	}
	return stored
}

// encodeAtRestValue 加密可逆凭据：启用外部 KEK 时用 enc:v2:，否则沿用旧 enc:v1:。
func (h *handlers) encodeAtRestValue(plain string) (string, error) {
	if h.box.Enabled() {
		return h.box.Seal(plain)
	}
	return encryptAgentToken(h.agentKeySecret(), plain)
}

// MigrateSecretsAtRest 启动时的幂等数据迁移（无需 SQL 迁移即可重复执行）。
// mode: auto（默认，明文/v1 → v2）/ decrypt（回滚，v2 → 旧格式）/ off（跳过）。
func MigrateSecretsAtRest(ctx context.Context, db *sql.DB, box *secretBox, legacySecret, mode string, log *slog.Logger) {
	if db == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "off":
		return
	case "decrypt":
		rewrapSecrets(ctx, db, box, legacySecret, false, log)
		return
	}
	if !box.Enabled() {
		if log != nil {
			log.Warn("未启用静态加密（PP_SECRET_KEY / PP_SECRET_KEY_FILE 不可用），密钥仍按旧格式存储")
		}
		return
	}
	rewrapSecrets(ctx, db, box, legacySecret, true, log)
}

// secretSettingKeys 需要静态加密的 app_settings 键（与 settings 加密白名单保持一致）。
func secretSettingKeys() map[string]bool {
	keys := map[string]bool{
		"jwt_secret":       true,
		"agent_key_secret": true,
	}
	for _, d := range settingDefs {
		if d.Secret {
			keys[d.Key] = true
		}
	}
	return keys
}

func rewrapSecrets(ctx context.Context, db *sql.DB, box *secretBox, legacySecret string, encrypt bool, log *slog.Logger) {
	keys := secretSettingKeys()
	rows, err := db.QueryContext(ctx, "SELECT `key`, value FROM app_settings")
	if err == nil {
		type kv struct{ k, v string }
		var list []kv
		for rows.Next() {
			var k string
			var v sql.NullString
			if rows.Scan(&k, &v) == nil {
				list = append(list, kv{k, v.String})
			}
		}
		rows.Close()
		for _, item := range list {
			if !keys[item.k] || strings.TrimSpace(item.v) == "" {
				continue
			}
			plain := decodeAnyAtRest(item.v, box, legacySecret)
			if plain == "" {
				continue
			}
			var next string
			var err2 error
			if encrypt {
				if strings.HasPrefix(item.v, secretAtRestVersion) {
					continue // 已是目标格式
				}
				next, err2 = box.Seal(plain)
			} else {
				if !strings.HasPrefix(item.v, secretAtRestVersion) {
					continue // 不是 v2，无需回滚
				}
				// jwt_secret / agent_key_secret 旧代码是「直接当明文读」的，回滚必须写明文；
				// 其它 Secret 设置旧代码支持 enc:v1: 解密，写 v1 即可。
				if item.k == "jwt_secret" || item.k == "agent_key_secret" {
					next = plain
				} else {
					next = legacyFormatForRollback(plain, legacySecret)
				}
			}
			if err2 != nil {
				if log != nil {
					log.Warn("重加密设置失败", "key", item.k, "err", err2)
				}
				continue
			}
			if _, err := db.ExecContext(ctx,
				"UPDATE app_settings SET value = ?, updated_at = UNIX_TIMESTAMP()*1000 WHERE `key` = ?",
				next, item.k); err != nil && log != nil {
				log.Warn("写回设置失败", "key", item.k, "err", err)
			}
		}
	}

	// servers.agent_token_enc：加密副本（真实鉴权用哈希，不影响节点）。
	if srows, err := db.QueryContext(ctx, "SELECT id, COALESCE(agent_token_enc,'') FROM servers WHERE COALESCE(agent_token_enc,'') <> ''"); err == nil {
		type sv struct {
			id  int64
			enc string
		}
		var list []sv
		for srows.Next() {
			var id int64
			var enc string
			if srows.Scan(&id, &enc) == nil {
				list = append(list, sv{id, enc})
			}
		}
		srows.Close()
		for _, item := range list {
			plain := decodeAnyAtRest(item.enc, box, legacySecret)
			if plain == "" {
				continue
			}
			var next string
			if encrypt {
				if strings.HasPrefix(item.enc, secretAtRestVersion) {
					continue
				}
				var err error
				next, err = box.Seal(plain)
				if err != nil {
					continue
				}
			} else {
				if !strings.HasPrefix(item.enc, secretAtRestVersion) {
					continue
				}
				next = legacyFormatForRollback(plain, legacySecret)
			}
			_, _ = db.ExecContext(ctx, "UPDATE servers SET agent_token_enc = ? WHERE id = ?", next, item.id)
		}
	}

	// vpn_certificates：私钥存 key_pem_enc（迁移 0050），旧列 key_pem 清空/恢复。
	rewrapVPNKeyPEM(ctx, db, box, legacySecret, encrypt, log)
}

// rewrapVPNKeyPEM 处理 vpn_certificates.key_pem ↔ key_pem_enc。
func rewrapVPNKeyPEM(ctx context.Context, db *sql.DB, box *secretBox, legacySecret string, encrypt bool, log *slog.Logger) {
	rows, err := db.QueryContext(ctx,
		"SELECT id, COALESCE(key_pem,''), COALESCE(key_pem_enc,'') FROM vpn_certificates WHERE id = 1")
	if err != nil {
		return // 表/列不存在（尚未执行迁移 0050）时跳过
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var keyPEM, keyEnc string
		if rows.Scan(&id, &keyPEM, &keyEnc) != nil {
			continue
		}
		if encrypt {
			if keyEnc != "" {
				continue
			}
			plain := decodeAnyAtRest(keyPEM, box, legacySecret)
			if plain == "" || strings.HasPrefix(plain, "enc:") {
				continue
			}
			sealed, err := box.Seal(plain)
			if err != nil {
				continue
			}
			if _, err := db.ExecContext(ctx,
				"UPDATE vpn_certificates SET key_pem = '', key_pem_enc = ? WHERE id = ?", sealed, id); err != nil && log != nil {
				log.Warn("加密 TLS 私钥失败", "err", err)
			}
		} else {
			if keyEnc == "" {
				continue
			}
			plain := box.Open(keyEnc)
			if plain == "" {
				continue
			}
			if _, err := db.ExecContext(ctx,
				"UPDATE vpn_certificates SET key_pem = ?, key_pem_enc = NULL WHERE id = ?", plain, id); err != nil && log != nil {
				log.Warn("回滚 TLS 私钥失败", "err", err)
			}
		}
	}
}

// decodeAnyAtRest 用 box / 旧 secret 依次尝试解密，失败时返回原始明文（兼容初始状态）。
func decodeAnyAtRest(stored string, box *secretBox, legacySecret string) string {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return ""
	}
	if strings.HasPrefix(stored, secretAtRestVersion) {
		if box != nil {
			return box.Open(stored)
		}
		return ""
	}
	if strings.HasPrefix(stored, agentKeyVersion) {
		return decryptAgentToken(legacySecret, stored)
	}
	return stored
}

// legacyFormatForRollback 回滚时把值写回旧二进制可读的格式（优先 enc:v1:）。
func legacyFormatForRollback(plain, legacySecret string) string {
	if strings.TrimSpace(legacySecret) != "" {
		if enc, err := encryptAgentToken(legacySecret, plain); err == nil {
			return enc
		}
	}
	return plain
}