package api

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"strings"
	"testing"
)

// 自签名后按 RFC 6376 重新计算摘要，验证 DKIM 签名能被正确校验。
// 这能防止「签名写了但算错」——那种情况对端只会静默判 spam，本地看不到任何报错。
func TestDKIMSignVerifiable(t *testing.T) {
	kp, err := generateDKIMKey()
	if err != nil {
		t.Fatalf("生成密钥失败：%v", err)
	}

	raw := []byte("From: ProxyPilot <noreply@example.com>\r\n" +
		"To: <user@example.org>\r\n" +
		"Subject: =?utf-8?q?=E6=B5=8B=E8=AF=95?=\r\n" +
		"Date: Mon, 10 Nov 2026 10:00:00 +0800\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n" +
		"\r\n" +
		"<p>hello</p>\r\n")

	signed, err := dkimSign(raw, "example.com", "pp", kp.PrivatePEM)
	if err != nil {
		t.Fatalf("签名失败：%v", err)
	}
	if !strings.HasPrefix(string(signed), "DKIM-Signature:") {
		t.Fatalf("签名头没有加在最前面")
	}

	// 解析签名头
	end := strings.Index(string(signed), "\r\n\r\n")
	if end < 0 {
		t.Fatal("找不到头/体分隔")
	}
	headText := string(signed)[:end]
	body := signed[end+4:]

	var sigHeader string
	for _, line := range strings.SplitAfter(headText, "\r\n") {
		if strings.HasPrefix(line, "DKIM-Signature:") || strings.HasPrefix(line, "\t") {
			sigHeader += line
		} else if sigHeader != "" {
			break
		}
	}
	if sigHeader == "" {
		t.Fatal("没找到 DKIM-Signature 头")
	}

	tags := map[string]string{}
	for _, part := range strings.Split(strings.ReplaceAll(strings.TrimPrefix(sigHeader, "DKIM-Signature:"), "\r\n", ""), ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.Index(part, "="); i > 0 {
			tags[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
		}
	}

	// 1) 校验 bh（正文摘要）
	wantBH := base64.StdEncoding.EncodeToString(func() []byte {
		s := sha256.Sum256(canonBodyRelaxed(body))
		return s[:]
	}())
	if tags["bh"] != wantBH {
		t.Fatalf("正文摘要不匹配\n got=%s\nwant=%s", tags["bh"], wantBH)
	}

	// 2) 用修正后的签名头重算并验签
	signedHeaders := strings.Split(tags["h"], ":")
	headerMap := parseHeaderMap(headText)

	var toSign strings.Builder
	for _, name := range signedHeaders {
		toSign.WriteString(canonHeaderRelaxed(name, headerMap[name]))
	}
	// 重新构造 DKIM 头（b= 清空）用于验签
	unsigned := "DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed;\r\n" +
		"\td=" + tags["d"] + "; s=" + tags["s"] + ";\r\n" +
		"\tt=" + tags["t"] + ";\r\n" +
		"\th=" + tags["h"] + ";\r\n" +
		"\tbh=" + tags["bh"] + ";\r\n" +
		"\tb="
	toSign.WriteString(canonDKIMHeader(unsigned))

	digest := sha256.Sum256([]byte(toSign.String()))
	sig, err := base64.StdEncoding.DecodeString(tags["b"])
	if err != nil {
		t.Fatalf("签名 base64 解码失败：%v", err)
	}

	// 从私钥推出公钥
	priv, err := parseRSAPrivateKey(kp.PrivatePEM)
	if err != nil {
		t.Fatalf("解析私钥失败：%v", err)
	}
	if err := rsa.VerifyPKCS1v15(&priv.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("DKIM 验签失败：%v", err)
	}
}

// 公钥字符串要能还原成 PKCS#1 RSA 公钥（DNS 记录里就是这个）。
func TestDKIMPublicKeyRoundTrip(t *testing.T) {
	kp, err := generateDKIMKey()
	if err != nil {
		t.Fatalf("生成密钥失败：%v", err)
	}
	pub, err := dkimPublicFromPrivate(kp.PrivatePEM)
	if err != nil {
		t.Fatalf("导出公钥失败：%v", err)
	}
	if pub != kp.PublicB64 {
		t.Fatal("导出的公钥与生成时不一致")
	}
	der, err := base64.StdEncoding.DecodeString(pub)
	if err != nil {
		t.Fatalf("公钥 base64 解码失败：%v", err)
	}
	if _, err := x509.ParsePKCS1PublicKey(der); err != nil {
		t.Fatalf("公钥不是合法 PKCS#1：%v", err)
	}
	// 控制台表单用的值：单行、无内部引号
	rec := dkimDNSRecord(pub)
	if !strings.HasPrefix(rec, "v=DKIM1;") || !strings.Contains(rec, "p=") {
		t.Fatalf("TXT 记录格式不对：%s", rec[:40])
	}
	if strings.Contains(rec, `"`) {
		t.Fatalf("控制台表单值不能带引号：%s", rec[:60])
	}
	if strings.Contains(rec, " ") && strings.Contains(rec, `" `) {
		t.Fatal("不应包含分段引号")
	}

	// BIND 格式：每段 255 字符以内、各自加引号
	bind := dkimDNSRecordBind(pub)
	for _, seg := range strings.Split(bind, `" "`) {
		s := strings.Trim(seg, `"`)
		if len(s) > 255 {
			t.Fatalf("BIND 分段超过 255 字符：%d", len(s))
		}
	}
	if !strings.HasPrefix(bind, `"`) {
		t.Fatalf("BIND 格式应以引号开头：%s", bind[:20])
	}
}

// 拆地址与域名。
func TestSplitMailAddress(t *testing.T) {
	cases := []struct {
		in, addr, domain string
		wantErr          bool
	}{
		{"noreply@proxypilot.yangzifun.org", "noreply@proxypilot.yangzifun.org", "proxypilot.yangzifun.org", false},
		{"ProxyPilot <a@b.com>", "a@b.com", "b.com", false},
		{"bad", "", "", true},
		{"@nodomain.com", "", "", true},
	}
	for _, c := range cases {
		addr, domain, err := splitMailAddress(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q 应该报错", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q 意外报错：%v", c.in, err)
			continue
		}
		if addr != c.addr || domain != c.domain {
			t.Errorf("%q → (%s,%s)，期望 (%s,%s)", c.in, addr, domain, c.addr, c.domain)
		}
	}
}