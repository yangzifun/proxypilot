package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// 可在后台“系统设置”里覆盖的键（未设置时回落环境变量）。
const (
	settingPublicBaseURL   = "site_public_base_url"
	settingAgentAccessURL  = "agent_access_url"
	settingAgentInternalURL = "agent_internal_url"
	settingAgentAccessURLs = "agent_access_urls"
	settingAgentMeshEnabled = "agent_mesh_enabled"
	settingAgentMeshCenter = "agent_mesh_center"
	settingAgentMeshSync = "agent_mesh_sync"
	settingAgentBinaryBase = "agent_binary_base_url"
	settingComponentBase   = "component_base_url"
	// sing-box 组件的版本与各架构 sha256：安装时下发给节点做校验
	settingComponentVersion = "component_version"
	settingComponentSHA     = "component_sha"
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
	// 自建直投（builtin）
	settingMailDomain     = "mail_domain"
	settingMailDKIMSelect = "mail_dkim_selector"
	settingMailDKIMKey    = "mail_dkim_private"
	settingMailEgressIP   = "mail_egress_ip"
	settingMailDKIMBits   = "mail_dkim_bits"
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
	{Key: settingAgentInternalURL},
	{Key: settingAgentAccessURLs},
	{Key: settingAgentMeshEnabled, Bool: true},
	{Key: settingAgentMeshCenter},
	{Key: settingAgentMeshSync, Bool: true},
	{Key: settingAgentBinaryBase},
	{Key: settingComponentBase},
	{Key: settingComponentVersion},
	{Key: settingComponentSHA + "_amd64"},
	{Key: settingComponentSHA + "_arm64"},
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
	{Key: settingMailDomain},
	{Key: settingMailDKIMSelect},
	{Key: settingMailDKIMKey, Secret: true},
	{Key: settingMailEgressIP},
	{Key: settingMailDKIMBits},
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
	case settingSMTPPass, settingOIDCSecret, settingTurnstileSecret, settingCFToken, settingR2SecretKey,
		settingMailDKIMKey:
		return true
	}
	return false
}

// superOnlySettings 安全敏感设置键：只有超级管理员可以「修改」。
//
// 背景（审计 P2-14）：/api/settings 之前对所有管理员开放，普通管理员只要改
// agent_binary_base_url / component_base_url，再点一次「一键升级 Agent」，
// 就能让全部节点安装任意二进制 —— 这是「面板普通管理员 → 全部节点 root」的提权链。
//
// 分类标准只有一条：这个键能不能改变「面板信任谁 / 把什么交给节点」。
// 普通管理员仍可保存其它设置；这里只拦「实际发生变化的敏感键」，
// 设置页整表单 PUT（敏感项未改动）不会被误伤。
var superOnlySettings = map[string]bool{
	// 1) 分发基址与组件校验值：改这里等于向全部节点下发任意二进制
	settingAgentBinaryBase:         true,
	settingComponentBase:           true,
	settingComponentVersion:        true,
	settingComponentSHA + "_amd64": true,
	settingComponentSHA + "_arm64": true,
	// 2) Agent 接入 / 组网端点：改了能把 Agent 引到伪造面板，等价窃取接入密钥与配置
	settingAgentAccessURL:   true,
	settingAgentInternalURL: true,
	settingAgentAccessURLs:  true,
	settingAgentMeshEnabled: true,
	settingAgentMeshCenter:  true,
	settingAgentMeshSync:    true,
	// 3) 登录信任链：改 OIDC 身份源 / 登录方式可绕过或劫持管理员认证
	settingPasswordEnabled: true,
	settingEmailCode:       true,
	settingOIDCEnabled:     true,
	settingOIDCIssuer:      true,
	settingOIDCClientID:    true,
	settingOIDCSecret:      true,
	settingOIDCScopes:      true,
	settingOIDCRedirect:    true,
	// 4) 凭证类（明文或 AES-GCM 加密存储的密钥）
	settingTurnstileSecret: true,
	settingSMTPPass:        true,
	settingCFToken:         true,
	settingMailDKIMKey:     true,
	settingR2AccessKey:     true,
	settingR2SecretKey:     true,
	// 5) 对外访问基址：影响邮件里的登录 / 验证链接与 OAuth 回调拼接
	settingPublicBaseURL: true,
}

// settingDefByKey 提供 key → settingDef 的只读索引（Bool 判定需要）。
var settingDefByKey = func() map[string]settingDef {
	out := make(map[string]settingDef, len(settingDefs))
	for _, d := range settingDefs {
		out[d.Key] = d
	}
	return out
}()

func isSuperOnlySetting(key string) bool { return superOnlySettings[key] }

// settingEffectiveString 返回与设置页展示一致的当前值：
// 已显式配置则取配置值（敏感项解密后），否则回落环境变量 / 内置默认值。
func (h *handlers) settingEffectiveString(ctx context.Context, key string) string {
	if v := strings.TrimSpace(h.settingValue(ctx, key)); v != "" {
		return v
	}
	return strings.TrimSpace(h.settingDefault(key))
}

// sensitiveSettingChanged 判断本次提交是否真的改动了敏感键。
//
// 设置页会把整张表单（含未修改项）一起 PUT，所以不能看到敏感键就拒绝；
// 布尔项按 0/1 归一化后比较，Agent 数据同步地址按 JSON 数组语义比较，
// 其余按去空白字符串比较。
func (h *handlers) sensitiveSettingChanged(ctx context.Context, key, submitted string) bool {
	def, ok := settingDefByKey[key]
	if !ok {
		return true
	}
	if def.Bool {
		next, ok := boolString(submitted)
		if !ok {
			return true
		}
		cur, ok := boolString(h.settingEffectiveString(ctx, key))
		if !ok {
			return true
		}
		return next != cur
	}
	submitted = strings.TrimSpace(submitted)
	current := h.settingEffectiveString(ctx, key)
	if submitted == current {
		return false
	}
	if key == settingAgentAccessURLs {
		// 设置页把 JSON 数组解析再序列化，格式可能与库里的原串不同（空格/顺序），按语义比较。
		normalize := func(s string) string {
			var list []string
			if json.Unmarshal([]byte(s), &list) != nil {
				return s
			}
			b, err := json.Marshal(list)
			if err != nil {
				return s
			}
			return string(b)
		}
		return normalize(submitted) != normalize(current)
	}
	return true
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
	CFToken, CFAccountID string
	// 自建直投
	Domain      string
	Selector    string
	DKIMPrivate string
	PublicB64   string
	EgressIP    string
	// DKIMBits 1024 或 2048
	DKIMBits int
}

func (m mailConfig) dkimSelector() string {
	if strings.TrimSpace(m.Selector) != "" {
		return strings.TrimSpace(m.Selector)
	}
	return dkimDefaultSelector
}

func (m mailConfig) BuiltinReady() bool {
	return strings.TrimSpace(m.From) != "" && strings.TrimSpace(m.Domain) != ""
}

// messageID 生成 Message-ID（直投时对端用于去重与追踪）。
func (m mailConfig) messageID() string {
	d := strings.TrimSpace(m.Domain)
	if d == "" {
		return ""
	}
	return fmt.Sprintf("%d.%s@%s", timeNowUnixNano(), randomHex(8), d)
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
		Domain:      h.setting(ctx, settingMailDomain, ""),
		Selector:    h.setting(ctx, settingMailDKIMSelect, dkimDefaultSelector),
		DKIMPrivate: h.setting(ctx, settingMailDKIMKey, ""),
		EgressIP:    h.setting(ctx, settingMailEgressIP, ""),
		DKIMBits:    int(intOrJS(h.setting(ctx, settingMailDKIMBits, "2048"), 2048, 1024, 2048)),
	}
}

func (m mailConfig) SMTPReady() bool { return m.Addr != "" && (m.From != "") }
// ResolvedProvider 决定实际发信通道；auto 优先 Cloudflare，其次 SMTP。
func (m mailConfig) ResolvedProvider() string {
	if m.Provider == "builtin" && m.BuiltinReady() {
		return "builtin"
	}
	switch m.Provider {
	case "smtp", "builtin":
		return m.Provider
	}
	// auto：优先自建直投（无需第三方），否则 SMTP
	if m.BuiltinReady() && strings.TrimSpace(m.DKIMPrivate) != "" {
		return "builtin"
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
	case settingTurnstileSite:
		return strings.TrimSpace(h.cfg.TurnstileSiteKey)
	// 登录方式开关的「未显式配置」默认值就是运行期默认值（与 passwordLoginEnabled /
	// emailCodeLoginEnabled / effectiveOIDC 保持一致），否则设置页会把「默认开启」显示成关闭，
	// 普通管理员原样保存又会被敏感设置校验判成「改动」。
	case settingPasswordEnabled:
		if h.cfg.OIDCDisablePassword {
			return "0"
		}
		return "1"
	case settingEmailCode:
		return "1"
	case settingOIDCEnabled:
		return "1"
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
