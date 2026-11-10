// Package config 读取并校验服务端配置。
package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultJWTSecret 是 Worker 端的历史兜底密钥。Go 服务**拒绝**使用它启动，
// 避免出现“看起来能跑、实际谁都能伪造 token”的部署。
const DefaultJWTSecret = "your-default-jwt-secret-change-this"

// Config 是进程级配置，全部来自环境变量。
type Config struct {
	// Addr HTTP 监听地址，例如 ":8080"。
	Addr string
	// AgentInternalAddr 内网 Agent 专用监听地址（明文，仅 Agent 接口），例如 100.96.0.18:8080。
	// 留空表示不额外监听：Agent 直接使用主监听（:80）经由组网访问。
	AgentInternalAddr string
	// MySQLDSN 形如 user:pass@tcp(127.0.0.1:3306)/proxypilot?parseTime=false&loc=UTC
	MySQLDSN string
	// JWTSecret 与 Worker 共用；沿用同一密钥即可让旧 token 继续有效。
	JWTSecret string
	// SuperAdmin 超级管理员用户名。
	SuperAdmin string

	// AdminTokenTTL 管理端登录有效期；PortalTokenTTL 用户中心登录有效期。
	AdminTokenTTL  time.Duration
	PortalTokenTTL time.Duration
	// AdminPassword 首次启动时若 admin 仍是默认口令，用它重置（生产必配）。
	AdminPassword string
	// AutoMigrate 启动时自动执行 migrations/ 下未应用的 SQL。
	AutoMigrate bool
	// TurnstileSecret 为空时跳过人机验证（与 Worker 的宽松语义一致）。
	TurnstileSecret string
	// AllowDevSecret 允许在本地开发时使用短密钥（仅 PP_DEV=1 时）。
	AllowDevSecret bool
	// LegacyBase 未迁移接口的转发目标（例如 https://proxypilot.example.com）。
	// 为空时未迁移接口返回 501。
	LegacyBase string
	// SMTP 配置；全为空时发送验证码接口返回 503。
	SMTPAddr string
	SMTPUser string
	SMTPPass string
	MailFrom string

	// LoginAlertWebhook / LoginAlertEmail 登录锁定告警出口（可选）。
	LoginAlertWebhook string
	LoginAlertEmail   string
	// MailProvider 发信通道：auto / smtp / cloudflare（默认 auto）。
	MailProvider string
	// Cloudflare 相关配置（R2 与 DNS 记录写入）。
	CloudflareAPIToken  string
	CloudflareAccountID string
	// OIDCDisablePassword 为 true 时禁用密码登录（与 Worker 的 OIDC_DISABLE_PASSWORD 对应）。
	OIDCDisablePassword bool
	OIDCIssuer          string
	OIDCClientID        string
	OIDCClientSecret    string
	OIDCScopes          string
	OIDCProviderName    string
	OIDCButtonText      string
	OIDCRedirectURI     string
	// AgentKeySecret 加密 Agent 接入令牌（可逆）；为空时回落 JWT_SECRET。
	AgentKeySecret string
	// AgentInstallBase 安装脚本分发基址（对象存储），为空时使用本站 /api/agent/install.sh。
	AgentInstallBase string
	// BucketDir 本地对象存储目录（替代 R2/S3）；为空时使用 server/data/bucket。
	BucketDir string
	// AgentDistDir Agent 二进制 / manifest / 安装脚本的本地目录；为空时使用 agent/dist。
	AgentDistDir string
	// R2（S3 兼容）对象存储，用于附件管理 / Agent 分发。
	R2AccountID       string
	R2Bucket          string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2Endpoint        string
	// CDNBaseURL R2 公共自定义域（如 https://cdn.example.com），用于生成对象外链。
	CDNBaseURL string
	// PublicBaseURL 对外访问基址（如 https://pp.example.com）；本地模式用它拼接 /agent-dist 下载地址。
	PublicBaseURL string
	// WebDist 可选：自定义前端构建目录；为空时使用内嵌生产 bundle（从 worker 导出）。
	WebDist string
	// TurnstileSiteKey 前端人机验证 site key（secret 走 TurnstileSecret）。
	TurnstileSiteKey string
	// AgentBinaryBaseURL Agent 二进制与 manifest.json 的基址。
	AgentBinaryBaseURL string
}

// Load 从环境变量加载配置。
func Load() (Config, error) {
	c := Config{
		Addr:                env("PP_ADDR", ":8080"),
		AgentInternalAddr:   strings.TrimSpace(os.Getenv("PP_AGENT_INTERNAL_ADDR")),
		MySQLDSN:            strings.TrimSpace(os.Getenv("PP_MYSQL_DSN")),
		JWTSecret:           os.Getenv("PP_JWT_SECRET"),
		SuperAdmin:          env("PP_SUPER_ADMIN", "admin"),
		AdminTokenTTL:       envHours("PP_ADMIN_TOKEN_TTL_HOURS", 168),
		PortalTokenTTL:      envHours("PP_PORTAL_TOKEN_TTL_HOURS", 720),
		AdminPassword:       strings.TrimSpace(os.Getenv("PP_ADMIN_PASSWORD")),
		AutoMigrate:         envBool("PP_AUTO_MIGRATE", true),
		TurnstileSecret:     os.Getenv("PP_TURNSTILE_SECRET"),
		TurnstileSiteKey:    firstNonEmpty(os.Getenv("PP_TURNSTILE_SITE_KEY"), os.Getenv("TURNSTILE_SITE_KEY")),
		AllowDevSecret:      envBool("PP_DEV", false),
		LegacyBase:          strings.TrimRight(strings.TrimSpace(os.Getenv("PP_LEGACY_BASE")), "/"),
		SMTPAddr:            strings.TrimSpace(os.Getenv("PP_SMTP_ADDR")),
		SMTPUser:            os.Getenv("PP_SMTP_USER"),
		SMTPPass:            os.Getenv("PP_SMTP_PASS"),
		MailFrom:            strings.TrimSpace(os.Getenv("PP_MAIL_FROM")),
		LoginAlertWebhook:   strings.TrimSpace(os.Getenv("PP_LOGIN_ALERT_WEBHOOK")),
		LoginAlertEmail:     strings.TrimSpace(os.Getenv("PP_LOGIN_ALERT_EMAIL")),
		MailProvider:        firstNonEmpty(os.Getenv("PP_MAIL_PROVIDER"), "auto"),
		CloudflareAPIToken:  firstNonEmpty(os.Getenv("PP_CLOUDFLARE_API_TOKEN"), os.Getenv("CLOUDFLARE_API_TOKEN")),
		CloudflareAccountID: firstNonEmpty(os.Getenv("PP_CLOUDFLARE_ACCOUNT_ID"), os.Getenv("CLOUDFLARE_ACCOUNT_ID")),
			// 兼容 Worker 的环境变量名，便于直接沿用线上配置
		OIDCDisablePassword: envBool("PP_OIDC_DISABLE_PASSWORD", envBool("OIDC_DISABLE_PASSWORD", false)),
		OIDCIssuer:          firstNonEmpty(os.Getenv("PP_OIDC_ISSUER"), os.Getenv("OIDC_ISSUER")),
		OIDCClientID:        firstNonEmpty(os.Getenv("PP_OIDC_CLIENT_ID"), os.Getenv("OIDC_CLIENT_ID")),
		OIDCClientSecret:    firstNonEmpty(os.Getenv("PP_OIDC_CLIENT_SECRET"), os.Getenv("OIDC_CLIENT_SECRET")),
		OIDCScopes:          firstNonEmpty(os.Getenv("PP_OIDC_SCOPES"), os.Getenv("OIDC_SCOPES")),
		OIDCProviderName:    firstNonEmpty(os.Getenv("PP_OIDC_PROVIDER_NAME"), os.Getenv("OIDC_PROVIDER_NAME")),
		OIDCButtonText:      firstNonEmpty(os.Getenv("PP_OIDC_BUTTON_TEXT"), os.Getenv("OIDC_BUTTON_TEXT")),
		OIDCRedirectURI:     firstNonEmpty(os.Getenv("PP_OIDC_REDIRECT_URI"), os.Getenv("OIDC_REDIRECT_URI")),
		AgentKeySecret:      firstNonEmpty(os.Getenv("PP_AGENT_KEY_SECRET"), os.Getenv("AGENT_KEY_SECRET")),
		AgentInstallBase:    strings.TrimRight(firstNonEmpty(os.Getenv("PP_AGENT_INSTALL_BASE"), os.Getenv("AGENT_INSTALL_BASE")), "/"),
		AgentBinaryBaseURL:  strings.TrimRight(firstNonEmpty(os.Getenv("PP_AGENT_BINARY_BASE_URL"), os.Getenv("AGENT_BINARY_BASE_URL")), "/"),
		WebDist:             strings.TrimRight(os.Getenv("PP_WEB_DIST"), "/"),
		BucketDir:           env("PP_BUCKET_DIR", "server/data/bucket"),
		AgentDistDir:        env("PP_AGENT_DIST_DIR", "agent/dist"),
		R2AccountID:         strings.TrimSpace(os.Getenv("PP_R2_ACCOUNT_ID")),
		R2Bucket:            strings.TrimSpace(os.Getenv("PP_R2_BUCKET")),
		R2AccessKeyID:       strings.TrimSpace(os.Getenv("PP_R2_ACCESS_KEY_ID")),
		R2SecretAccessKey:   strings.TrimSpace(os.Getenv("PP_R2_SECRET_ACCESS_KEY")),
		R2Endpoint:          strings.TrimRight(strings.TrimSpace(os.Getenv("PP_R2_ENDPOINT")), "/"),
		CDNBaseURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("PP_CDN_BASE_URL")), "/"),
		PublicBaseURL:       strings.TrimRight(firstNonEmpty(os.Getenv("PP_PUBLIC_BASE_URL"), os.Getenv("PUBLIC_BASE_URL")), "/"),
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.MySQLDSN == "" {
		return errors.New("PP_MYSQL_DSN 未设置")
	}
	// JWT 密钥允许为空或弱值：启动后会从 app_settings 读取持久化密钥，
	// 若仍不可用则在 main 中拒绝启动（见 enforcePersistentSecrets）。
	if strings.TrimSpace(c.SuperAdmin) == "" {
		return errors.New("PP_SUPER_ADMIN 不能为空")
	}
	return nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

// envHours 读取小时数环境变量并转为 Duration（<=0 时使用默认值）。
func envHours(key string, defHours int) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return time.Duration(defHours) * time.Hour
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return time.Duration(defHours) * time.Hour
	}
	return time.Duration(n) * time.Hour
}
