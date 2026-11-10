package api

import (
	"testing"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

func TestRandomPasswordShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw := randomPassword(12)
		if len(pw) != 12 {
			t.Fatalf("长度应为 12，实际 %d：%q", len(pw), pw)
		}
		for _, c := range pw {
			if !containsByte(pwdAlphabet, byte(c)) {
				t.Fatalf("出现不在字母表里的字符 %q（%q）", c, pw)
			}
		}
		// 不应包含易混淆字符
		for _, bad := range []byte{'0', 'O', '1', 'l', 'I'} {
			if containsByte(pw, bad) {
				t.Fatalf("不应该包含易混淆字符 %q：%q", bad, pw)
			}
		}
		if seen[pw] {
			t.Fatalf("生成了重复密码：%q", pw)
		}
		seen[pw] = true
	}
}

func TestRandomPasswordMinLength(t *testing.T) {
	if got := randomPassword(0); len(got) != 12 {
		t.Fatalf("n=0 时应回退为 12 位，实际 %d", len(got))
	}
}

func containsByte(s string, b byte) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return true
		}
	}
	return false
}

func TestValidatePortalPassword(t *testing.T) {
	cases := []struct {
		pw    string
		wantOK bool
	}{
		{"Abcdefg2", true},
		{"abcdefg2", true},
		{"Ab1", false},              // 太短
		{" Abcdefg2", false},        // 首部空格
		{"Abcdefg2 ", false},        // 尾部空格
		{"", false},                 // 空
		{string(make([]byte, 80)), false}, // 过长（含 \x00 但长度超限优先报错）
	}
	for _, c := range cases {
		err := validatePortalPassword(c.pw)
		if c.wantOK && err != nil {
			t.Errorf("%q 应该通过，却报错：%v", c.pw, err)
		}
		if !c.wantOK && err == nil {
			t.Errorf("%q 应该被拒绝", c.pw)
		}
	}
}

// 初始密码必须是能通过 VerifyPassword 的合法哈希。
func TestInitialPasswordHashRoundTrip(t *testing.T) {
	pw := randomPassword(12)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		t.Fatalf("哈希失败：%v", err)
	}
	ok, _ := auth.VerifyPassword(hash, pw)
	if !ok {
		t.Fatal("初始密码无法通过校验")
	}
	if bad, _ := auth.VerifyPassword(hash, pw+"x"); bad {
		t.Fatal("错误密码不应通过")
	}
}