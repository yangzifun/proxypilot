//go:build integration

package api

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// 用户中心 SSO 的身份映射逻辑（不起 IdP，只验「OIDC 拿到的邮箱 → 用户令牌」这一段）。
//
//	PP_TEST_DSN='root:proxypilot@tcp(127.0.0.1:3306)/pp_test?charset=utf8mb4&loc=UTC' \
//	  go test -tags integration ./internal/api -run TestIntegrationPortalOIDC -v
func TestIntegrationPortalOIDC(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("PP_TEST_DSN")
	if dsn == "" {
		t.Skip("未设置 PP_TEST_DSN，跳过集成测试")
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("连接 MySQL 失败: %v", err)
	}
	defer pool.Close()
	if _, err := db.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	h := &handlers{
		db:  pool,
		cfg: config.Config{JWTSecret: "integration-secret-123456", PortalTokenTTL: 3600 * 1e9},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	const uuid = "99999999-8888-7777-6666-555555555555"
	const email = "sso-test@example.com"
	_, _ = pool.ExecContext(ctx, `DELETE FROM uuid_info WHERE uuid = ?`, uuid)
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO uuid_info (uuid, owner, email, email_norm, status, created_at, updated_at)
		 VALUES (?, 'sso', ?, ?, 'active', 1, 1)`, uuid, email, email); err != nil {
		t.Fatalf("插入用户失败: %v", err)
	}
	defer pool.ExecContext(ctx, `DELETE FROM uuid_info WHERE uuid = ?`, uuid)

	req := httptest.NewRequest("GET", "/api/oidc/callback", nil).WithContext(ctx)

	// 1) 已绑定邮箱 → 应签出用户令牌
	token, gotUUID, reason := h.finishPortalOIDC(req, email)
	if token == "" {
		t.Fatalf("已绑定邮箱应能登录，却返回 reason=%q", reason)
	}
	if gotUUID != uuid {
		t.Fatalf("uuid 不匹配：want %s got %s", uuid, gotUUID)
	}

	// 2) 未绑定邮箱 → no_uuid_binding
	if _, _, r2 := h.finishPortalOIDC(req, "nobody@example.com"); r2 != "no_uuid_binding" {
		t.Fatalf("未绑定邮箱应返回 no_uuid_binding，实际 %q", r2)
	}

	// 3) 停用账号 → account_disabled
	if _, err := pool.ExecContext(ctx,
		`UPDATE uuid_info SET status = 'inactive' WHERE uuid = ?`, uuid); err != nil {
		t.Fatal(err)
	}
	if _, _, r3 := h.finishPortalOIDC(req, email); r3 != "account_disabled" {
		t.Fatalf("停用账号应返回 account_disabled，实际 %q", r3)
	}

	// 4) 空邮箱 → email_missing
	if _, _, r4 := h.finishPortalOIDC(req, ""); r4 != "email_missing" {
		t.Fatalf("空邮箱应返回 email_missing，实际 %q", r4)
	}
}