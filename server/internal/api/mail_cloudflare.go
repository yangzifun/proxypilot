package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Cloudflare Email Sending REST API（不依赖 Workers 绑定）。
// 文档：POST /accounts/{account_id}/email/sending/send
var cloudflareEmailEndpoint = "https://api.cloudflare.com/client/v4/accounts/%s/email/sending/send"

var htmlTagRe = regexp.MustCompile(`(?s)<[^>]+>`)
var spacesRe = regexp.MustCompile(`\s+`)

func htmlToText(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = strings.ReplaceAll(s, "<br />", "\n")
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spacesRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func (h *handlers) sendMailCloudflare(mc mailConfig, to, subject, htmlBody string) error {
	if !mc.CloudflareReady() {
		return errMailNotConfigured
	}
	text := htmlToText(htmlBody)
	if text == "" {
		text = subject
	}
	payload, _ := json.Marshal(map[string]any{
		"to": to, "from": mc.CFFrom, "subject": subject, "html": htmlBody, "text": text,
	})
	endpoint := fmt.Sprintf(cloudflareEmailEndpoint, url.PathEscape(mc.CFAccountID))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return errors.New("Cloudflare 邮件请求创建失败")
	}
	req.Header.Set("Authorization", "Bearer "+mc.CFToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare 邮件请求失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var out struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result struct {
			Delivered        []string `json:"delivered"`
			Queued           []string `json:"queued"`
			PermanentBounces []string `json:"permanent_bounces"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !out.Success {
		msg := strings.TrimSpace(string(raw))
		if len(out.Errors) > 0 {
			parts := make([]string, 0, len(out.Errors))
			for _, e := range out.Errors {
				parts = append(parts, fmt.Sprintf("%d:%s", e.Code, e.Message))
			}
			msg = strings.Join(parts, "; ")
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("Cloudflare 邮件发送失败（HTTP %d）：%s", resp.StatusCode, msg)
	}
	return nil
}
