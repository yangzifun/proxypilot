package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"

	"fmt"
	"mime"
	"net"
	"net/smtp"
	"sort"
	"strings"
	"time"
)

// 自建直投（builtin）：不经过任何第三方，自己解析收件域 MX 后连 25 端口投递。
//
// 适用场景：面板跑在自己的 VPS 上，有独立域名与 PTR，愿意自己维护发信信誉。
// 需要在 DNS 配好 SPF / DKIM / DMARC，否则基本进垃圾箱。

// timeNowUnixNano 供 Message-ID 使用。
func timeNowUnixNano() int64 { return time.Now().UnixNano() }

// randomHex 生成 n 字节的十六进制串（Message-ID 的随机部分）。
func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// mxHost 是解析出来的收件服务器。
type mxHost struct {
	Host string
	Pref uint16
}

// resolveMX 解析收件域的 MX；没有 MX 时按 RFC 5321 回落到 A 记录。
func resolveMX(domain string) ([]mxHost, error) {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil, fmt.Errorf("收件域为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	mxs, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err == nil && len(mxs) > 0 {
		out := make([]mxHost, 0, len(mxs))
		for _, m := range mxs {
			host := strings.TrimSuffix(m.Host, ".")
			if host == "" {
				continue
			}
			out = append(out, mxHost{Host: host, Pref: m.Pref})
		}
		if len(out) > 0 {
			sort.Slice(out, func(i, j int) bool { return out[i].Pref < out[j].Pref })
			return out, nil
		}
	}

	// 无 MX：直接投递到 A/AAAA（RFC 5321 §5.1）
	ips, ierr := net.DefaultResolver.LookupHost(ctx, domain)
	if ierr == nil && len(ips) > 0 {
		return []mxHost{{Host: domain, Pref: 0}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("解析 %s 的 MX 失败：%v", domain, err)
	}
	return nil, fmt.Errorf("%s 没有可用的 MX 记录", domain)
}

// buildSignedMessage 组装 MIME 并在需要时补 DKIM 签名。
// SMTP 与自建直投共用，保证两条通道产出的报文一致。
func buildSignedMessage(mc mailConfig, to, subject, htmlBody string) ([]byte, error) {
	from := strings.TrimSpace(mc.From)
	fromAddr, fromDomain, err := splitMailAddress(from)
	if err != nil {
		return nil, err
	}
	_ = fromAddr
	raw := buildMIMEMessage(mc.From, to, subject, htmlBody, mc)

	// 只有 From 域与配置的发信域一致时才签，否则签名会因 d= 不匹配失效
	if strings.TrimSpace(mc.DKIMPrivate) != "" && strings.TrimSpace(mc.Domain) != "" &&
		strings.EqualFold(fromDomain, strings.TrimSpace(mc.Domain)) {
		signed, serr := dkimSign(raw, mc.Domain, mc.dkimSelector(), mc.DKIMPrivate)
		if serr != nil {
			return raw, serr
		}
		return signed, nil
	}
	return raw, nil
}

// sendMailBuiltin 直连收件方 MX 投递。
func (h *handlers) sendMailBuiltin(mc mailConfig, to, subject, htmlBody string) error {
	if !mc.BuiltinReady() {
		return fmt.Errorf("自建直投未配置：请在「邮件」里填写发信域名与发件人")
	}
	from := strings.TrimSpace(mc.From)
	fromAddr, fromDomain, err := splitMailAddress(from)
	if err != nil {
		return err
	}
	toAddr, toDomain, err := splitMailAddress(to)
	if err != nil {
		return fmt.Errorf("收件人地址无效：%w", err)
	}

	raw, serr := buildSignedMessage(mc, to, subject, htmlBody)
	if serr != nil {
		h.log.Warn("DKIM 签名失败，改为未签名投递", "err", serr)
	}

	mxs, err := resolveMX(toDomain)
	if err != nil {
		return err
	}

	var lastErr error
	for _, mx := range mxs {
		if err := deliverSMTP(mx.Host, fromAddr, toAddr, fromDomain, raw); err != nil {
			lastErr = fmt.Errorf("%s: %w", mx.Host, err)
			h.log.Warn("直投失败，尝试下一个 MX", "mx", mx.Host, "err", err)
			continue
		}
		h.log.Info("邮件已直投", "to", maskEmailForLog(toAddr), "mx", mx.Host, "domain", toDomain)
		return nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的收件服务器")
	}
	return fmt.Errorf("直投失败：%w（常见原因：云厂商封禁出站 25 端口，或对方拒收本机 IP）", lastErr)
}

// deliverSMTP 与单个 MX 完成一次投递。
func deliverSMTP(mxHost, from, to, heloDomain string, msg []byte) error {
	addr := net.JoinHostPort(mxHost, "25")
	dialer := &net.Dialer{Timeout: 12 * time.Second}
	conn, err := dialer.Dial("tcp", addr)
	if err != nil {
		return fmt.Errorf("连接 25 端口失败：%w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))

	c, err := smtp.NewClient(conn, mxHost)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("SMTP 握手失败：%w", err)
	}
	defer c.Close()

	if heloDomain != "" {
		_ = c.Hello(heloDomain)
	}
	// 对端支持就升级到 TLS；不支持也继续（默认不强制）
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: mxHost, MinVersion: tls.VersionTLS12}); err != nil {
			// STARTTLS 失败时重新建连再走明文，避免半开状态
			_ = c.Close()
			return fmt.Errorf("STARTTLS 失败：%w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("MAIL FROM 被拒：%w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO 被拒：%w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("DATA 被拒：%w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("写入邮件失败：%w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交邮件失败：%w", err)
	}
	_ = c.Quit()
	return nil
}

// buildMIMEMessage 组装 RFC 5322 报文（CRLF 行尾、中文主题用 Q 编码）。
func buildMIMEMessage(from, to, subject, htmlBody string, mc mailConfig) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: ProxyPilot <%s>\r\n", from)
	fmt.Fprintf(&b, "To: <%s>\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	if id := mc.messageID(); id != "" {
		fmt.Fprintf(&b, "Message-ID: <%s>\r\n", id)
		b.WriteString("Auto-Submitted: auto-generated\r\n")
	}
	b.WriteString("\r\n")
	// 正文统一 CRLF，且单个点行要转义（SMTP 的 dot-stuffing 由 net/smtp 处理）
	body := strings.ReplaceAll(htmlBody, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	b.WriteString(body)
	return b.Bytes()
}

// splitMailAddress 取出地址与域名两部分。
func splitMailAddress(s string) (addr, domain string, err error) {
	s = strings.TrimSpace(s)
	// 支持 "Name <a@b>" 形式
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			s = s[i+1 : i+j]
		}
	}
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return "", "", fmt.Errorf("邮箱格式无效：%s", s)
	}
	addr = s
	domain = strings.TrimSuffix(s[at+1:], ".")
	if !strings.Contains(domain, ".") {
		return "", "", fmt.Errorf("邮箱域名无效：%s", s)
	}
	return addr, domain, nil
}

// maskEmailForLog 只保留首字符与域名，避免日志泄露完整地址。
func maskEmailForLog(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at <= 1 {
		return "***"
	}
	return addr[:1] + "***" + addr[at:]
}