package config

import (
	"testing"
	"time"
)

// 审计中-07：默认令牌有效期收紧（管理端 24h / 门户 7d），仍可用环境变量覆盖。
func TestTokenTTLDefaultsAndOverrides(t *testing.T) {
	t.Setenv("PP_MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/proxypilot")
	t.Setenv("PP_ADMIN_TOKEN_TTL_HOURS", "")
	t.Setenv("PP_PORTAL_TOKEN_TTL_HOURS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if cfg.AdminTokenTTL != 24*time.Hour {
		t.Fatalf("AdminTokenTTL = %v，期望 24h", cfg.AdminTokenTTL)
	}
	if cfg.PortalTokenTTL != 168*time.Hour {
		t.Fatalf("PortalTokenTTL = %v，期望 168h", cfg.PortalTokenTTL)
	}

	t.Setenv("PP_ADMIN_TOKEN_TTL_HOURS", "6")
	t.Setenv("PP_PORTAL_TOKEN_TTL_HOURS", "48")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load 失败：%v", err)
	}
	if cfg.AdminTokenTTL != 6*time.Hour || cfg.PortalTokenTTL != 48*time.Hour {
		t.Fatalf("环境变量覆盖未生效：admin=%v portal=%v", cfg.AdminTokenTTL, cfg.PortalTokenTTL)
	}
}