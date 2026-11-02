package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// verifyTurnstile 校验人机验证。未配置 secret 时直接放行（与 Worker 的
// verifyTurnstile 宽松实现一致；不再复刻 Worker 中“空 token 一律 400”的问题）。
func (h *handlers) verifyTurnstile(r *http.Request, token string) error {
	secret := h.turnstileSecret(r.Context())
	if secret == "" {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("请完成人机验证")
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://challenges.cloudflare.com/turnstile/v0/siteverify",
		strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("人机验证服务不可用，请稍后重试")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errors.New("人机验证服务不可用，请稍后重试")
	}
	defer resp.Body.Close()

	var out struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || !out.Success {
		return errors.New("人机验证失败")
	}
	return nil
}
