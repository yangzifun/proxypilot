package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var errMailNotConfigured = errors.New("邮件服务未配置：请在后台配置自建直投 / Cloudflare Email Sending / SMTP")

// sendMail 统一发信入口：按配置选择 Cloudflare Email Sending 或 SMTP。
func (h *handlers) sendMail(to, subject, htmlBody string) error {
	mc := h.effectiveMail(context.Background())
	switch mc.ResolvedProvider() {
	case "cloudflare":
		return h.sendMailCloudflare(mc, to, subject, htmlBody)
	case "smtp":
		return h.sendMailSMTP(mc, to, subject, htmlBody)
	case "builtin":
		return h.sendMailBuiltin(mc, to, subject, htmlBody)
	default:
		return errMailNotConfigured
	}
}

// sendMailSMTP 通过 SMTP（STARTTLS）发送 HTML 邮件。
func (h *handlers) sendMailSMTP(mc mailConfig, to, subject, htmlBody string) error {
	if !mc.SMTPReady() {
		return errMailNotConfigured
	}
	host, _, err := net.SplitHostPort(mc.Addr)
	if err != nil {
		host = mc.Addr
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", mc.From)
	fmt.Fprintf(&buf, "To: %s\r\n", to)
	fmt.Fprintf(&buf, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	buf.WriteString("MIME-Version: 1.0\r\n")
	buf.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	buf.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	buf.WriteString(htmlBody)

	c, err := smtp.Dial(mc.Addr)
	if err != nil {
		return fmt.Errorf("连接 SMTP 失败: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("STARTTLS 失败: %w", err)
		}
	}
	if mc.User != "" {
		if err := c.Auth(smtp.PlainAuth("", mc.User, mc.Pass, host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := c.Mail(mc.From); err != nil {
		return fmt.Errorf("SMTP MAIL FROM 失败: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("SMTP RCPT TO 失败: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA 失败: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("写入邮件正文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	return c.Quit()
}

// emailHTML 与 Worker 的邮件排版保持同一观感（不追求逐像素一致）。
func emailHTML(title, intro, code string, ttlMinutes int) string {
	var b strings.Builder
	b.WriteString(`<!doctype html><html><body style="margin:0;background:#f5f6fa;font-family:-apple-system,Segoe UI,Roboto,Helvetica,Arial,sans-serif">`)
	b.WriteString(`<div style="max-width:520px;margin:0 auto;padding:32px 20px">`)
	b.WriteString(`<div style="background:#fff;border-radius:14px;padding:32px 28px;box-shadow:0 8px 24px rgba(15,23,42,.06)">`)
	b.WriteString(`<h2 style="margin:0 0 8px;font-size:20px;color:#0f172a">` + title + `</h2>`)
	b.WriteString(`<div style="margin:0 0 20px;font-size:14px;line-height:1.8;color:#475569">` + intro + `</div>`)
	if code != "" {
		b.WriteString(`<div style="border:1px dashed #c7d2fe;background:#eef2ff;border-radius:12px;padding:18px;text-align:center">`)
		b.WriteString(`<span style="font-size:30px;letter-spacing:10px;font-weight:700;color:#4338ca">` + code + `</span>`)
		b.WriteString(`</div>`)
	}
	if ttlMinutes > 0 {
		b.WriteString(`<p style="margin:18px 0 0;font-size:13px;color:#64748b">验证码 ` + fmt.Sprint(ttlMinutes) + ` 分钟内有效。若非本人操作，请忽略本邮件。</p>`)
	}
	b.WriteString(`<p style="margin:22px 0 0;font-size:12px;color:#94a3b8">ProxyPilot 自动通知，请勿直接回复。</p>`)
	b.WriteString(`</div></div></body></html>`)
	return b.String()
}
