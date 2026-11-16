//go:build integration

package api_test

import (
	"context"
	"os"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// TestIntegrationVPNProtocolColumns 校验迁移 0058 的协议能力列与会话 protocol 落库：
//
//	PP_TEST_DSN='root:proxypilot@tcp(127.0.0.1:3306)/pp_test?charset=utf8mb4&loc=UTC' \
//	  go test -tags integration ./internal/api -run TestIntegrationVPNProtocolColumns -v
//
// 未设置 PP_TEST_DSN 时跳过（与现有集成测试一致）。会执行全部迁移，请用独立测试库。
func TestIntegrationVPNProtocolColumns(t *testing.T) {
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

	const testServerID = int64(990001)
	cleanup := func() {
		_, _ = pool.ExecContext(ctx,
			`DELETE FROM vpn_sessions WHERE node_id IN (SELECT id FROM vpn_nodes WHERE server_id = ?)`,
			testServerID)
		_, _ = pool.ExecContext(ctx, `DELETE FROM vpn_nodes WHERE server_id = ?`, testServerID)
	}
	cleanup()
	defer cleanup()

	// 1) 旧风格插入（不写协议列）→ 默认值必须是「仅私有协议」
	res, err := pool.ExecContext(ctx,
		`INSERT INTO vpn_nodes
		   (server_id, name, domain, region, endpoint, mesh_ip4, client_cidr4, node_pubkey,
		    state, listen_port, capacity, cert_mode, version, last_seen, created_at, updated_at)
		 VALUES (?, 'it-protocol', 'it.example.com', 'IT', 'it.example.com:443',
		    '10.99.0.250', '10.88.99.0/24', '', 'active', 443, 10, 'internal_ca', '', 0, 0, 0)`,
		testServerID)
	if err != nil {
		t.Fatalf("插入测试节点失败: %v", err)
	}
	nodeID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("获取节点 ID 失败: %v", err)
	}

	var priv, masq, port int
	var path, auth, state string
	if err := pool.QueryRowContext(ctx,
		`SELECT private_enabled, masque_enabled, masque_path, masque_port, masque_auth, masque_state
		   FROM vpn_nodes WHERE id = ?`, nodeID).
		Scan(&priv, &masq, &path, &port, &auth, &state); err != nil {
		t.Fatalf("旧数据默认协议列读取失败（迁移 0058 未生效？）: %v", err)
	}
	if priv != 1 {
		t.Fatalf("旧数据 private_enabled = %d，期望 1", priv)
	}
	if masq != 0 {
		t.Fatalf("旧数据 masque_enabled = %d，期望 0", masq)
	}
	if state != "disabled" {
		t.Fatalf("旧数据 masque_state = %q，期望 disabled", state)
	}

	// 2) 面板启用 MASQUE（等价 applyNodeProtocols 的写入）→ 读回一致
	if _, err := pool.ExecContext(ctx,
		`UPDATE vpn_nodes SET private_enabled = 1, masque_enabled = 1, masque_path = '/masque',
		   masque_port = 8443, masque_auth = 'bearer', masque_state = 'ready' WHERE id = ?`, nodeID); err != nil {
		t.Fatalf("写入协议能力失败: %v", err)
	}
	if err := pool.QueryRowContext(ctx,
		`SELECT private_enabled, masque_enabled, masque_path, masque_port, masque_auth, masque_state
		   FROM vpn_nodes WHERE id = ?`, nodeID).
		Scan(&priv, &masq, &path, &port, &auth, &state); err != nil {
		t.Fatalf("读取协议能力失败: %v", err)
	}
	if priv != 1 || masq != 1 || path != "/masque" || port != 8443 || auth != "bearer" || state != "ready" {
		t.Fatalf("协议能力读回不一致：priv=%d masq=%d path=%q port=%d auth=%q state=%q",
			priv, masq, path, port, auth, state)
	}

	// 3) 会话 protocol：旧风格插入默认 private，更新为 masque 后可读回
	sres, err := pool.ExecContext(ctx,
		`INSERT INTO vpn_sessions (device_id, node_id, grant_id, ip4, ip6, client_ip,
		   started_at, ended_at, bytes_tx, bytes_rx, packets, close_reason)
		 VALUES (1, ?, 0, '10.88.99.10', '', '127.0.0.1', 0, 0, 0, 0, 0, '')`, nodeID)
	if err != nil {
		t.Fatalf("插入测试会话失败: %v", err)
	}
	sessionID, err := sres.LastInsertId()
	if err != nil {
		t.Fatalf("获取会话 ID 失败: %v", err)
	}
	var proto string
	if err := pool.QueryRowContext(ctx,
		`SELECT protocol FROM vpn_sessions WHERE id = ?`, sessionID).Scan(&proto); err != nil {
		t.Fatalf("会话 protocol 列读取失败（迁移 0058 未生效？）: %v", err)
	}
	if proto != "private" {
		t.Fatalf("旧会话 protocol = %q，期望 private", proto)
	}
	if _, err := pool.ExecContext(ctx,
		`UPDATE vpn_sessions SET protocol = 'masque' WHERE id = ?`, sessionID); err != nil {
		t.Fatalf("更新会话协议失败: %v", err)
	}
	if err := pool.QueryRowContext(ctx,
		`SELECT protocol FROM vpn_sessions WHERE id = ?`, sessionID).Scan(&proto); err != nil {
		t.Fatalf("会话 protocol 读回失败: %v", err)
	}
	if proto != "masque" {
		t.Fatalf("会话 protocol = %q，期望 masque", proto)
	}
}
