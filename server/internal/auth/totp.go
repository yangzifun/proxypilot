package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// VerifyTOTP 校验 6 位 TOTP 验证码，允许前后各 skew 个 30 秒窗口。
func VerifyTOTP(secret, code string, skew int) bool {
	return verifyTOTPAt(secret, code, time.Now(), skew)
}

func verifyTOTPAt(secret, code string, now time.Time, skew int) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	norm := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(norm)
	if err != nil || len(key) == 0 {
		return false
	}
	counter := uint64(now.Unix() / 30)
	for i := -skew; i <= skew; i++ {
		if hotp6(key, counter+uint64(i)) == code {
			return true
		}
	}
	return false
}

func hotp6(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[off])&0x7f)<<24 |
		uint32(sum[off+1])<<16 |
		uint32(sum[off+2])<<8 |
		uint32(sum[off+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}

// GenerateTOTPSecret 生成 20 位 base32 密钥（与 Worker 的 TOTP.generateSecret 等价）。
func GenerateTOTPSecret() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	out := make([]byte, 20)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// OTPAuthURL 生成 otpauth:// 链接（issuer 默认 YZFN Admin，与 Worker 一致）。
func OTPAuthURL(username, secret, issuer string) string {
	if issuer == "" {
		issuer = "YZFN Admin"
	}
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer), url.PathEscape(username), secret, url.QueryEscape(issuer))
}
