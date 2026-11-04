package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// loginAlertPayload 登录锁定告警体（Webhook JSON）。
type loginAlertPayload struct {
	Event      string `json:"event"`
	Username   string `json:"username"`
	IP         string `json:"ip"`
	Key        string `json:"key"`
	OccurredAt int64  `json:"occurred_at"`
	Message    string `json:"message"`
}

// alertLoginLockout 在账号+IP 触发登录锁定时异步推送 Webhook / 邮件。
func (h *handlers) alertLoginLockout(key string) {
	ip, user := "", key
	if i := strings.Index(key, "|"); i > 0 {
		ip, user = key[:i], key[i+1:]
	}
	payload := loginAlertPayload{
		Event: "login.lockout", Username: user, IP: ip, Key: key,
		OccurredAt: time.Now().UnixMilli(),
		Message:    fmt.Sprintf("登录失败次数过多已锁定：账号 %s，来源 %s", user, ip),
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if hook := strings.TrimSpace(h.cfg.LoginAlertWebhook); hook != "" {
			body, _ := json.Marshal(payload)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, bytes.NewReader(body))
			if err == nil {
				req.Header.Set("Content-Type", "application/json")
				if resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req); err == nil {
					_ = resp.Body.Close()
				}
			}
		}
		if to := strings.TrimSpace(h.cfg.LoginAlertEmail); to != "" {
			html := fmt.Sprintf("<p>%s</p><p>时间：%s</p>", payload.Message,
				time.UnixMilli(payload.OccurredAt).Format("2006-01-02 15:04:05"))
			_ = h.sendMail(to, "ProxyPilot 登录锁定告警", html)
		}
	}()
}
