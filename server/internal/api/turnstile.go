package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Turnstile 校验端点与客户端做成变量，便于单测注入。
var (
	turnstileSiteverifyURL = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	turnstileHTTPClient    = &http.Client{Timeout: 5 * time.Second}

	// turnstileMisconfigOnce 只把「secret 配置错误」告警一次，避免日志刷屏。
	turnstileMisconfigOnce sync.Once
)

// verifyTurnstile 校验人机验证。未配置 secret 时直接放行。
//
// 失败策略（可用性优先，安全边界交给限速/审计）：
//   - 用户 token 无效（invalid-input-response）→ 拒绝；
//   - secret 配置错误（invalid-input-secret / missing-input-secret）→ 记录 ERROR 一次并放行：
//     否则一次配置失误会把管理端/用户端所有登录锁死，且管理员无从在页面上自救；
//   - Cloudflare 网络错误/5xx → 记录 WARN 并放行：Cloudflare 故障不应导致全站无法登录。
//
// 该函数在账号查询之前执行，失败开放不会泄露「某邮箱是否已绑定」。
func (h *handlers) verifyTurnstile(r *http.Request, token string) error {
	secret := h.turnstileSecret(r.Context())
	if secret == "" {
		return nil
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("请完成人机验证")
	}

	ctx, cancel := contextWithTimeout(r, 5*time.Second)
	defer cancel()

	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, turnstileSiteverifyURL, strings.NewReader(form.Encode()))
	if err != nil {
		h.log.Warn("Turnstile 请求构造失败，降级放行", "err", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := turnstileHTTPClient.Do(req)
	if err != nil {
		// Cloudflare 不可达：登录不能被第三方服务拖死。
		h.log.Warn("Turnstile 服务不可达，降级放行（仍受限速约束）", "err", err)
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
	if resp.StatusCode >= 500 {
		h.log.Warn("Turnstile 服务异常，降级放行", "status", resp.StatusCode)
		return nil
	}

	var out struct {
		Success    bool     `json:"success"`
		ErrorCodes []string `json:"error-codes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		h.log.Warn("Turnstile 响应解析失败，降级放行", "err", err, "status", resp.StatusCode)
		return nil
	}
	if out.Success {
		return nil
	}
	for _, code := range out.ErrorCodes {
		switch code {
		case "invalid-input-secret", "missing-input-secret":
			turnstileMisconfigOnce.Do(func() {
				h.log.Error("Turnstile Secret 配置错误，已临时降级放行；请到后台「系统设置 → 人机验证」重新粘贴与 Site Key 配对的 Secret",
					"error_codes", strings.Join(out.ErrorCodes, ","))
			})
			return nil
		}
	}
	if len(out.ErrorCodes) == 0 {
		h.log.Warn("Turnstile 校验失败（无错误码），按普通验证失败返回")
	}
	return fmt.Errorf("人机验证失败，请刷新页面重试")
}
