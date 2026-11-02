//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/api"
	"github.com/yzfn/proxypilot/server/internal/config"
	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// TestIntegrationLoginAndSub 需要真实 MySQL：
//
//	PP_TEST_DSN='root:proxypilot@tcp(127.0.0.1:3306)/pp_test?charset=utf8mb4&loc=UTC' \
//	  go test -tags integration ./internal/api -run TestIntegrationLoginAndSub -v
//
// 注意：会执行 0001_init.sql，请使用独立测试库。
func TestIntegrationLoginAndSub(t *testing.T) {
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

	cfg := config.Config{JWTSecret: "integration-secret-123456", SuperAdmin: "admin"}
	srv := httptest.NewServer(api.New(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()

	// 1) 登录
	body := bytes.NewBufferString(`{"username":"admin","password":"password"}`)
	resp, err := http.Post(srv.URL+"/api/login", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("登录失败: %d %s", resp.StatusCode, raw)
	}
	var login struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil || login.Token == "" {
		t.Fatalf("token 解析失败: %v", err)
	}

	// 2) 准备分组与配置（直接写库，避开需要渲染的配置 CRUD）
	const uuid = "11111111-2222-3333-4444-555555555555"
	_, _ = pool.ExecContext(ctx, `DELETE FROM configs WHERE uuid = ?`, uuid)
	_, _ = pool.ExecContext(ctx, `DELETE FROM uuid_info WHERE uuid = ?`, uuid)
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO uuid_info (uuid, owner, email, status, created_at, updated_at)
		 VALUES (?, '测试', 'it@example.com', 'active', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000)`, uuid); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO configs (uuid, config_data, protocol, created_at, updated_at)
		 VALUES (?, 'vless://it@example.com:443#it', 'vless', UNIX_TIMESTAMP()*1000, UNIX_TIMESTAMP()*1000)`, uuid); err != nil {
		t.Fatal(err)
	}

	// 3) 订阅输出
	resp2, err := http.Get(srv.URL + "/sub/" + uuid)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	raw, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("订阅失败: %d %s", resp2.StatusCode, raw)
	}
	decoded, err := base64.StdEncoding.DecodeString(stringsTrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("订阅不是合法 base64: %v", err)
	}
	if !bytes.Contains(decoded, []byte("vless://it@example.com:443#it")) {
		t.Fatalf("订阅内容不符: %s", decoded)
	}

	// 4) 分组列表（需要 JWT）
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/uuids?q=11111111", nil)
	req.Header.Set("Authorization", "Bearer "+login.Token)
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp3.Body)
		t.Fatalf("分组列表失败: %d %s", resp3.StatusCode, raw)
	}
}

func stringsTrimSpace(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
