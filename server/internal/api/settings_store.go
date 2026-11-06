package api

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"time"
)

// 可在后台“系统设置”里覆盖的键（未设置时回落环境变量）。
const (
	settingPublicBaseURL   = "site_public_base_url"
	settingAgentAccessURL  = "agent_access_url"
	settingAgentBinaryBase = "agent_binary_base_url"
	settingComponentBase   = "component_base_url"
	settingPasswordEnabled = "auth_password_enabled"
	settingEmailCode       = "auth_email_code_enabled"
	settingOIDCEnabled     = "auth_oidc_enabled"
	settingTurnstileSite   = "turnstile_site_key"
	settingTurnstileSecret = "turnstile_secret"
	settingSMTPAddr        = "smtp_addr"
	settingSMTPUser        = "smtp_user"
	settingSMTPPass        = "smtp_pass"
	settingMailFrom        = "mail_from"
	settingMailProvider    = "mail_provider"
	settingCFToken         = "cloudflare_api_token"
	settingCFAccount       = "cloudflare_account_id"
	settingCFFrom          = "cloudflare_from"
	settingR2Account       = "r2_account_id"
	settingR2Bucket        = "r2_bucket"
	settingR2AccessKey     = "r2_access_key_id"
	settingR2SecretKey     = "r2_secret_access_key"
	settingR2Endpoint      = "r2_endpoint"
	settingCDNBaseURL      = "cdn_base_url"
	settingOIDCIssuer      = "oidc_issuer"
	settingOIDCClientID    = "oidc_client_id"
	settingOIDCSecret      = "oidc_client_secret"
	settingOIDCScopes      = "oidc_scopes"
	settingOIDCProvider    = "oidc_provider_name"
	settingOIDCButton      = "oidc_button_text"
	settingOIDCRedirect    = "oidc_redirect_uri"
)

type settingDef struct {
	Key    string
	Secret bool
	Bool   bool
}

var settingDefs = []settingDef{
	{Key: settingPublicBaseURL},
	{Key: settingAgentAccessURL},
	{Key: settingAgentBinaryBase},
	{Key: settingComponentBase},
	{Key: settingPasswordEnabled, Bool: true},
	{Key: settingEmailCode, Bool: true},
	{Key: settingOIDCEnabled, Bool: true},
	{Key: settingTurnstileSite},
	{Key: settingTurnstileSecret, Secret: true},
	{Key: settingSMTPAddr},
	{Key: settingSMTPUser},
	{Key: settingSMTPPass, Secret: true},
	{Key: settingMailFrom},
	{Key: settingMailProvider},
	{Key: settingCFToken, Secret: true},
	{Key: settingCFAccount},
	{Key: settingCFFrom},
	{Key: settingR2Account},
	{Key: settingR2Bucket},
	{Key: settingR2AccessKey},
	{Key: settingR2SecretKey, Secret: true},
	{Key: settingR2Endpoint},
	{Key: settingCDNBaseURL},
	{Key: settingOIDCIssuer},
	{Key: settingOIDCClientID},
	{Key: settingOIDCSecret, Secret: true},
	{Key: settingOIDCScopes},
	{Key: settingOIDCProvider},
	{Key: settingOIDCButton},
	{Key: settingOIDCRedirect},

	// 通知 / 巡检 / 保留期（与 Worker app_settings 同名）
	{Key: "monitor_notify_enabled", Bool: true},
	{Key: "monitor_notify_emails"},
	{Key: "monitor_notify_cooldown_min"},
	{Key: "uuid_reconcile_interval_min"},
	{Key: "batch_retention_days"},
	{Key: "agent_task_retention_days"},
	{Key: "metrics_retention_days"},
}

// loadAppSettings 读取 app_settings（30 秒缓存）。
func (h *handlers) loadAppSettings(ctx context.Context) map[string]string {
	h.settingsMu.RLock()
	cache, at := h.settings, h.settingsAt
	h.settingsMu.RUnlock()
	if cache != nil && time.Since(at) < 30*time.Second {
		return cache
	}
	out := map[string]string{}
	if h.db != nil {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		rows, err := h.db.QueryContext(cctx, "SELECT `key`, value FROM app_settings")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var k string
				var v sql.NullString
				if rows.Scan(&k, &v) == nil && v.Valid {
					out[k] = v.String
				}
			}
		}
	}
	h.settingsMu.Lock()
	h.settings, h.settingsAt = out, time.Now()
	h.settingsMu.Unlock()
	return out
}

func (h *handlers) settingValue(ctx context.Context, key string) string {
	v := strings.TrimSpace(h.loadAppSettings(ctx)[key])
	if strings.HasPrefix(v, agentKeyVersion) {
		// 敏感设置以 AES-GCM 密文存储；解密失败视为未设置（回落环境变量）
		return decryptAgentToken(h.agentKeySecret(), v)
	}
	return v
}

func isSecretSetting(key string) bool {
	switch key {
	case settingSMTPPass, settingOIDCSecret, settingTurnstileSecret, settingCFToken, settingR2SecretKey:
		return true
	}
	return false
}

func (h *handlers) setting(ctx context.Context, key, fallback string) string {
	if v := h.settingValue(ctx, key); v != "" {
		return v
	}
	return fallback
}

func (h *handlers) settingBool(ctx context.Context, key string, fallback bool) bool {
	v := strings.ToLower(h.settingValue(ctx, key))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

func (h *handlers) invalidateSettings() {
	h.settingsMu.Lock()
	h.settingsAt = time.Time{}
	h.settingsMu.Unlock()
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

func (h *handlers) getSetting(ctx context.Context, key, fallback string) string {
	return h.setting(ctx, key, fallback)
}

func (h *handlers) putSetting(ctx context.Context, key, value string) error {
	return h.saveSetting(ctx, key, value)
}

func (h *handlers) saveSetting(ctx context.Context, key, value string) error {
	if value != "" && isSecretSetting(key) && !strings.HasPrefix(value, agentKeyVersion) {
		enc, err := encryptAgentToken(h.agentKeySecret(), value)
		if err != nil {
			return err
		}
		value = enc
	}
	_, err := h.db.ExecContext(ctx,
		"INSERT INTO app_settings (`key`, value, updated_at) VALUES (?, ?, ?) "+
			"ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
		key, value, nowMillis())
	return err
}

// ---- 登录 / 人机验证 / 邮件 / OIDC 的运行时配置 ----

func (h *handlers) turnstileSiteKey(ctx context.Context) string {
	return h.setting(ctx, settingTurnstileSite, strings.TrimSpace(h.cfg.TurnstileSiteKey))
}

func (h *handlers) turnstileSecret(ctx context.Context) string {
	return h.setting(ctx, settingTurnstileSecret, strings.TrimSpace(h.cfg.TurnstileSecret))
}

func (h *handlers) passwordLoginEnabled(ctx context.Context) bool {
	return h.settingBool(ctx, settingPasswordEnabled, !h.cfg.OIDCDisablePassword)
}

func (h *handlers) emailCodeLoginEnabled(ctx context.Context) bool {
	return h.settingBool(ctx, settingEmailCode, true)
}

type mailConfig struct {
	Provider                     string
	Addr, User, Pass, From       string
	CFToken, CFAccountID, CFFrom string
}

func (h *handlers) effectiveMail(ctx context.Context) mailConfig {
	return mailConfig{
		Provider:    strings.ToLower(h.setting(ctx, settingMailProvider, firstNonEmpty(strings.TrimSpace(h.cfg.MailProvider), "auto"))),
		Addr:        h.setting(ctx, settingSMTPAddr, strings.TrimSpace(h.cfg.SMTPAddr)),
		User:        h.setting(ctx, settingSMTPUser, h.cfg.SMTPUser),
		Pass:        h.setting(ctx, settingSMTPPass, h.cfg.SMTPPass),
		From:        h.setting(ctx, settingMailFrom, strings.TrimSpace(h.cfg.MailFrom)),
		CFToken:     h.setting(ctx, settingCFToken, strings.TrimSpace(h.cfg.CloudflareAPIToken)),
		CFAccountID: h.setting(ctx, settingCFAccount, strings.TrimSpace(h.cfg.CloudflareAccountID)),
		CFFrom:      h.setting(ctx, settingCFFrom, strings.TrimSpace(h.cfg.CloudflareFrom)),
	}
}

func (m mailConfig) SMTPReady() bool { return m.Addr != "" && (m.From != "") }
func (m mailConfig) CloudflareReady() bool {
	return m.CFToken != "" && m.CFAccountID != "" && m.CFFrom != ""
}

// ResolvedProvider 决定实际发信通道；auto 优先 Cloudflare，其次 SMTP。
func (m mailConfig) ResolvedProvider() string {
	switch m.Provider {
	case "smtp", "cloudflare":
		return m.Provider
	}
	if m.CloudflareReady() {
		return "cloudflare"
	}
	if m.SMTPReady() {
		return "smtp"
	}
	return ""
}

func (m mailConfig) Ready() bool { return m.ResolvedProvider() != "" }

type oidcRuntime struct {
	Issuer, ClientID, ClientSecret, Scopes, ProviderName, ButtonText, RedirectURI string
	Enabled                                                                       bool
	PasswordDisabled                                                              bool
}

func (h *handlers) effectiveOIDC(ctx context.Context) oidcRuntime {
	issuer := h.setting(ctx, settingOIDCIssuer, strings.TrimSpace(h.cfg.OIDCIssuer))
	clientID := h.setting(ctx, settingOIDCClientID, strings.TrimSpace(h.cfg.OIDCClientID))
	enabled := h.settingBool(ctx, settingOIDCEnabled, true) && issuer != "" && clientID != ""
	provider := h.setting(ctx, settingOIDCProvider, strings.TrimSpace(h.cfg.OIDCProviderName))
	button := h.setting(ctx, settingOIDCButton, strings.TrimSpace(h.cfg.OIDCButtonText))
	return oidcRuntime{
		Issuer: issuer, ClientID: clientID,
		ClientSecret: h.setting(ctx, settingOIDCSecret, strings.TrimSpace(h.cfg.OIDCClientSecret)),
		Scopes:       h.setting(ctx, settingOIDCScopes, strings.TrimSpace(h.cfg.OIDCScopes)),
		ProviderName: provider, ButtonText: button,
		RedirectURI:      h.setting(ctx, settingOIDCRedirect, strings.TrimSpace(h.cfg.OIDCRedirectURI)),
		Enabled:          enabled,
		PasswordDisabled: !h.passwordLoginEnabled(ctx),
	}
}

func (h *handlers) publicBaseURL(ctx context.Context) string {
	return strings.TrimRight(h.setting(ctx, settingPublicBaseURL, strings.TrimRight(h.cfg.PublicBaseURL, "/")), "/")
}

// settingDefault 返回环境变量/内置默认值，供设置页展示“未显式配置但已生效”的值。
func (h *handlers) settingDefault(key string) string {
	switch key {
	case settingPublicBaseURL:
		return strings.TrimSpace(h.cfg.PublicBaseURL)
	case settingAgentBinaryBase:
		return strings.TrimSpace(h.cfg.AgentBinaryBaseURL)
	case settingComponentBase:
		if v := strings.TrimRight(strings.TrimSpace(os.Getenv("PP_COMPONENT_BASE_URL")), "/"); v != "" {
			return v
		}
		base := strings.TrimSpace(h.cfg.AgentBinaryBaseURL)
		if base == "" {
			return ""
		}
		base = strings.TrimRight(base, "/")
		if strings.HasSuffix(base, "/agent") {
			return strings.TrimSuffix(base, "/agent") + "/singbox"
		}
		return base + "/singbox"
	case settingSMTPAddr:
		return strings.TrimSpace(h.cfg.SMTPAddr)
	case settingSMTPUser:
		return h.cfg.SMTPUser
	case settingMailFrom:
		return strings.TrimSpace(h.cfg.MailFrom)
	case settingMailProvider:
		return firstNonEmpty(strings.TrimSpace(h.cfg.MailProvider), "auto")
	case settingCFAccount:
		return strings.TrimSpace(h.cfg.CloudflareAccountID)
	case settingCFFrom:
		return strings.TrimSpace(h.cfg.CloudflareFrom)
	case settingTurnstileSite:
		return strings.TrimSpace(h.cfg.TurnstileSiteKey)
	case settingOIDCIssuer:
		return strings.TrimSpace(h.cfg.OIDCIssuer)
	case settingOIDCClientID:
		return strings.TrimSpace(h.cfg.OIDCClientID)
	case settingOIDCScopes:
		return strings.TrimSpace(h.cfg.OIDCScopes)
	case settingOIDCProvider:
		return strings.TrimSpace(h.cfg.OIDCProviderName)
	case settingOIDCButton:
		return strings.TrimSpace(h.cfg.OIDCButtonText)
	case settingOIDCRedirect:
		return strings.TrimSpace(h.cfg.OIDCRedirectURI)
	case settingR2Account:
		return strings.TrimSpace(h.cfg.R2AccountID)
	case settingR2Bucket:
		return strings.TrimSpace(h.cfg.R2Bucket)
	case settingR2AccessKey:
		return strings.TrimSpace(h.cfg.R2AccessKeyID)
	case settingR2Endpoint:
		return strings.TrimSpace(h.cfg.R2Endpoint)
	case settingCDNBaseURL:
		return strings.TrimSpace(h.cfg.CDNBaseURL)
	case settingSMTPPass:
		return h.cfg.SMTPPass
	case settingOIDCSecret:
		return strings.TrimSpace(h.cfg.OIDCClientSecret)
	case settingTurnstileSecret:
		return strings.TrimSpace(h.cfg.TurnstileSecret)
	case settingCFToken:
		return strings.TrimSpace(h.cfg.CloudflareAPIToken)
	case settingR2SecretKey:
		return strings.TrimSpace(h.cfg.R2SecretAccessKey)
	case "monitor_notify_enabled":
		return "0"
	}
	return ""
}
