package api

import (
	"strings"
	"testing"
)

func TestEmailHTMLWithoutCodeHasNoEmptyBox(t *testing.T) {
	body := emailHTML("服务器离线", "3 台服务器离线", "", 0)
	if strings.Contains(body, "letter-spacing") {
		t.Fatal("无验证码时不应出现验证码框")
	}
	if strings.Contains(body, "验证码 0 分钟") {
		t.Fatal("无验证码时不应出现 TTL 文案")
	}
	if !strings.Contains(body, "ProxyPilot 自动通知") {
		t.Fatal("缺少自动通知页脚")
	}
}

func TestEmailHTMLWithCodeContainsCode(t *testing.T) {
	body := emailHTML("登录验证码", "账号：admin", "123456", 10)
	if !strings.Contains(body, "123456") || !strings.Contains(body, "10 分钟") {
		t.Fatal("验证码邮件缺少验证码或有效期")
	}
}
