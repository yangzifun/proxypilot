package api

import (
	"os"
	"testing"
)

// 生成一封已签名的邮件写到临时文件，供外部实现（dkimpy）交叉验签。
// 只在 DKIM_DUMP 环境变量存在时写文件，平时不产生副作用。
func TestDumpSignedMessage(t *testing.T) {
	out := os.Getenv("DKIM_DUMP")
	if out == "" {
		t.Skip("未设置 DKIM_DUMP")
	}
	kp, err := generateDKIMKey(2048)
	if err != nil {
		t.Fatal(err)
	}

	html := emailHTML("邮件通道测试",
		"这封邮件由 ProxyPilot 面板发出，用于验证发信配置是否可用。<br>"+
			"发信通道：<b>builtin</b>；发件人：<b>noreply@proxypilot.yangzifun.org</b>。",
		"", 0)

	mc := mailConfig{Domain: "proxypilot.yangzifun.org", Selector: "pp", DKIMPrivate: kp.PrivatePEM}
	raw := buildMIMEMessage("noreply@proxypilot.yangzifun.org", "user@example.org",
		"ProxyPilot 邮件通道测试", html, mc)

	signed, err := dkimSign(raw, "proxypilot.yangzifun.org", "pp", kp.PrivatePEM)
	if err != nil {
		t.Fatal(err)
	}

	// 第一行写公钥，便于外部验签脚本读取
	content := "PUBKEY_B64:" + kp.PublicB64 + "\n---MSG---\n" + string(signed)
	if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已写出签名邮件到 %s（%d 字节）", out, len(signed))
}