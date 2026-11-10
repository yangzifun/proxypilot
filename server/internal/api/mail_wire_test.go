package api

import (
	"bufio"
	"bytes"
	"net/textproto"
	"os"
	"testing"
)

// 模拟 net/smtp 的 Data() writer（dot-stuffing + CRLF 规范化），
// 把「实际会发到网线上的字节」落盘，交给 dkimpy 交叉验签。
// 目的：区分「签名算错」与「传输过程改写了字节」。
func TestDumpWireBytes(t *testing.T) {
	out := os.Getenv("DKIM_WIRE")
	if out == "" {
		t.Skip("未设置 DKIM_WIRE")
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

	// 走一遍 bufio + textproto 的 dot writer，等价于 smtp.Client.Data()
	var wire bytes.Buffer
	bw := bufio.NewWriter(&wire)
	dw := textproto.NewWriter(bw).DotWriter()
	if _, err := dw.Write(signed); err != nil {
		t.Fatal(err)
	}
	if err := dw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := bw.Flush(); err != nil {
		t.Fatal(err)
	}

	content := "PUBKEY_B64:" + kp.PublicB64 + "\n---MSG---\n" + wire.String()
	if err := os.WriteFile(out, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已写出线上字节 %d（原始签名 %d）", wire.Len(), len(signed))
}