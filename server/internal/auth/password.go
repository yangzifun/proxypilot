// Package auth 提供口令哈希、JWT 与 TOTP。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	// HashPrefix 标识新格式口令哈希。
	HashPrefix = "pbkdf2_sha256"
	// HashIterations 是 PBKDF2 迭代次数。
	HashIterations = 210000

	saltLen = 16
	keyLen  = 32
)

// HashPassword 生成 pbkdf2_sha256$<iter>$<salt>$<hash> 格式的口令哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := pbkdf2Key([]byte(password), salt, HashIterations, keyLen)
	return fmt.Sprintf("%s$%d$%s$%s",
		HashPrefix, HashIterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk)), nil
}

// VerifyPassword 校验口令。
// 兼容 Worker 时代遗留的“无盐 SHA-256（64 位十六进制）”哈希，
// 并通过 needsRehash 告知调用方在登录成功后升级为新格式。
func VerifyPassword(stored, password string) (ok bool, needsRehash bool) {
	stored = strings.TrimSpace(stored)
	if strings.HasPrefix(stored, HashPrefix+"$") {
		parts := strings.Split(stored, "$")
		if len(parts) != 4 {
			return false, false
		}
		iter, err := strconv.Atoi(parts[1])
		if err != nil || iter <= 0 || iter > 5_000_000 {
			return false, false
		}
		salt, err := base64.RawStdEncoding.DecodeString(parts[2])
		if err != nil {
			return false, false
		}
		want, err := base64.RawStdEncoding.DecodeString(parts[3])
		if err != nil || len(want) == 0 {
			return false, false
		}
		got := pbkdf2Key([]byte(password), salt, iter, len(want))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			return false, false
		}
		return true, iter < HashIterations
	}

	// 遗留格式：hex(sha256(password))
	if len(stored) == 64 && isHex(stored) {
		sum := sha256.Sum256([]byte(password))
		got := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(got), []byte(strings.ToLower(stored))) != 1 {
			return false, false
		}
		return true, true
	}
	return false, false
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
