package api

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
// task-16：令牌轮换事故的「临时按来源 IP 放行 + 令牌自愈」测试
//
// 覆盖：
//  1. grace=1 且来源 IP 命中 → 放行（其余情况仍 401）；
//  2. 真实客户端 IP 来源（受信反代取 X-Forwarded-For）；
//  3. 正常令牌认证成功后自动撤销 grace；
//  4. 自愈任务同一节点只排一次，且令牌解密失败/可疑字符时不排；
//  5. 自愈脚本幂等（内容未变不重启）、不泄露令牌明文。
// =====================================================================

// agentServerTestColumns 必须与 agentServerSelectColumns 的字段顺序一致。
var agentServerTestColumns = []string{
	"id", "name", "link_mode", "enabled", "agent_next_poll_sec", "agent_version",
	"agent_platform", "agent_token_enc", "agent_last_poll_at", "agent_poll_count",
	"agent_last_seen", "last_status", "metrics_every", "allow_exec", "os_info", "tags",
	"username", "host", "port", "last_ip", "last_ip4", "last_ip6", "last_ip_private",
	"last_mesh_ip4", "last_mesh_ip6", "ip_country", "ip_region", "last_metrics",
	"agent_key_used_at",
}

// fakeAgentServerRow 构造一行 servers 查询结果，over 用来覆盖默认值。
func fakeAgentServerRow(over map[string]any) driver.Rows {
	def := map[string]any{
		"id": int64(8), "name": "彰化8", "link_mode": "agent", "enabled": int64(1),
		"agent_next_poll_sec": int64(60), "agent_version": "1.1", "agent_platform": "linux-amd64",
		"agent_token_enc": "", "agent_last_poll_at": int64(0), "agent_poll_count": int64(0),
		"agent_last_seen": int64(0), "last_status": "online", "metrics_every": int64(1),
		"allow_exec": int64(1), "os_info": "", "tags": "", "username": "root",
		"host": "node.example.com", "port": int64(22),
		"last_ip": "203.0.113.7", "last_ip4": "203.0.113.7", "last_ip6": "",
		"last_ip_private": "", "last_mesh_ip4": "100.96.0.8", "last_mesh_ip6": "",
		"ip_country": "", "ip_region": "", "last_metrics": "", "agent_key_used_at": int64(0),
	}
	for k, v := range over {
		def[k] = v
	}
	vals := make([]driver.Value, len(agentServerTestColumns))
	for i, col := range agentServerTestColumns {
		switch v := def[col].(type) {
		case int64:
			vals[i] = v
		case int:
			vals[i] = int64(v)
		case string:
			vals[i] = v
		}
	}
	return fakeRowsSet(agentServerTestColumns, vals)
}

func newGraceTestHandlers(t *testing.T) (*handlers, *fakeDB) {
	t.Helper()
	fdb, db := newFakeDB(t)
	return &handlers{db: db, log: testLogger(), cfg: config.Config{JWTSecret: "grace-test-secret"}}, fdb
}

func TestAgentAuthGraceAllowsMatchingIP(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	var graceArgs []any
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "agent_token_hash = ?"):
			return &fakeRows{}, nil // 旧令牌哈希查不到
		case strings.Contains(q, "agent_auth_grace = 1"):
			graceArgs = fakeArgsAny(args)
			return fakeAgentServerRow(nil), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }

	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "203.0.113.7:45678"
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("0", 64))
	srv, status, msg := h.agentAuthenticate(req, map[string]any{})
	if srv == nil || status != 0 || msg != "" {
		t.Fatalf("grace 放行失败：srv=%v status=%d msg=%q", srv, status, msg)
	}
	if !srv.GraceAuth {
		t.Fatal("grace 放行后 GraceAuth 必须为 true（poll 依赖它触发自愈）")
	}
	if srv.ID != 8 {
		t.Fatalf("server id = %d，期望 8", srv.ID)
	}
	if len(graceArgs) != 3 || graceArgs[0] != "203.0.113.7" {
		t.Fatalf("grace 查询应按来源 IP 匹配 3 个字段，实际参数 %#v", graceArgs)
	}
	if h.db == nil {
		t.Fatal("handlers.db 不应为 nil")
	}
}

func TestAgentAuthGraceRejectsUnknownIP(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		// 两种查询都查不到：令牌不匹配且 IP 不在 grace 列表
		return &fakeRows{}, nil
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "198.51.100.9:1234"
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("1", 64))
	srv, status, _ := h.agentAuthenticate(req, map[string]any{})
	if srv != nil || status != http.StatusUnauthorized {
		t.Fatalf("IP 未命中 grace 时应 401，实际 srv=%v status=%d", srv, status)
	}
}

// 受信反代（Cloudflare/回环）后面要取真实客户端 IP，而不是反代地址。
func TestAgentAuthGraceUsesRealClientIPBehindTrustedProxy(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	var gotIP any
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "agent_token_hash = ?"):
			return &fakeRows{}, nil
		case strings.Contains(q, "agent_auth_grace = 1"):
			if len(args) > 0 {
				gotIP = args[0]
			}
			return fakeAgentServerRow(nil), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }

	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "127.0.0.1:45678" // 可信反代
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("2", 64))
	srv, status, msg := h.agentAuthenticate(req, map[string]any{})
	if srv == nil || status != 0 {
		t.Fatalf("受信反代后的 grace 放行失败：status=%d msg=%q", status, msg)
	}
	if gotIP != "203.0.113.7" {
		t.Fatalf("应使用 X-Forwarded-For 里的真实客户端 IP，实际 %v", gotIP)
	}
}

func TestAgentAuthGraceDeniedWhenServerNotUsable(t *testing.T) {
	cases := map[string]map[string]any{
		"已禁用":        {"enabled": int64(0)},
		"非 agent 模式": {"link_mode": "ssh"},
	}
	for name, over := range cases {
		t.Run(name, func(t *testing.T) {
			h, fdb := newGraceTestHandlers(t)
			fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
				switch {
				case strings.Contains(q, "agent_token_hash = ?"):
					return &fakeRows{}, nil
				case strings.Contains(q, "agent_auth_grace = 1"):
					return fakeAgentServerRow(over), nil
				}
				return &fakeRows{}, nil
			}
			req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
			req.RemoteAddr = "203.0.113.7:45678"
			req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("3", 64))
			srv, status, _ := h.agentAuthenticate(req, map[string]any{})
			if srv != nil || status != http.StatusUnauthorized {
				t.Fatalf("不可用服务器不应放行：srv=%v status=%d", srv, status)
			}
		})
	}
}

func TestAgentAuthNormalTokenClearsGrace(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE(agent_auth_grace,0) FROM servers WHERE id = ?") {
			return fakeRowsSet([]string{"agent_auth_grace"}, []driver.Value{int64(1)}), nil
		}
		return fakeAgentServerRow(nil), nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }

	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "203.0.113.7:45678"
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("4", 64))
	srv, status, msg := h.agentAuthenticate(req, map[string]any{})
	if srv == nil || status != 0 {
		t.Fatalf("正常令牌认证失败：status=%d msg=%q", status, msg)
	}
	if srv.GraceAuth {
		t.Fatal("正常令牌认证不应标记 GraceAuth")
	}
	call, ok := fdb.lastExec("UPDATE servers SET agent_auth_grace = 0")
	if !ok {
		t.Fatal("正常令牌认证成功后必须自动撤销 agent_auth_grace")
	}
	if len(call.Args) < 2 || call.Args[1] != int64(8) {
		t.Fatalf("撤销 SQL 参数不对：%#v", call.Args)
	}
}

// grace 本来就是 0 时不应产生额外写库。
func TestAgentAuthNormalTokenNoGraceNoWrite(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE(agent_auth_grace,0) FROM servers WHERE id = ?") {
			return fakeRowsSet([]string{"agent_auth_grace"}, []driver.Value{int64(0)}), nil
		}
		return fakeAgentServerRow(nil), nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "203.0.113.7:45678"
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("5", 64))
	if srv, _, _ := h.agentAuthenticate(req, map[string]any{}); srv == nil {
		t.Fatal("正常认证失败")
	}
	if fdb.countExec("agent_auth_grace = 0") != 0 {
		t.Fatal("grace=0 的正常节点不应产生撤销写库")
	}
}

// 迁移 0053 还没应用（查询 agent_auth_grace 报错）时，正常 Agent 认证不能受影响。
func TestAgentAuthNormalTokenToleratesMissingGraceColumn(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE(agent_auth_grace,0)") {
			return nil, fmt.Errorf("Unknown column 'agent_auth_grace'")
		}
		return fakeAgentServerRow(nil), nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) { return fakeResult(1, 0), nil }
	req := httptest.NewRequest(http.MethodPost, "/api/agent/poll", nil)
	req.RemoteAddr = "203.0.113.7:45678"
	req.Header.Set("X-Agent-Token", "ag_"+strings.Repeat("6", 64))
	srv, status, msg := h.agentAuthenticate(req, map[string]any{})
	if srv == nil || status != 0 {
		t.Fatalf("迁移未应用时正常认证不应失败：status=%d msg=%q", status, msg)
	}
	if fdb.countExec("agent_auth_grace = 0") != 0 {
		t.Fatal("grace 列缺失时不应尝试写库")
	}
}

// 自愈任务只排一次：窗口内已有同 system_action 的任务就不再排。
func TestAgentTokenFixTaskQueuedOnlyOnce(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	h.cfg.AgentKeySecret = "agent-key-for-test"
	token := "ag_" + strings.Repeat("ab", 32)
	enc, err := encryptAgentToken(h.cfg.AgentKeySecret, token)
	if err != nil {
		t.Fatalf("加密测试令牌失败：%v", err)
	}
	srv := &agentServer{ID: 8, Name: "彰化8", LinkMode: "agent", GraceAuth: true, AgentTokenEnc: enc, NextPollSec: 60}

	var historyCount int64
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "payload LIKE '%agent_token_fix%'"):
			return fakeRowsSet([]string{"COUNT(*)"}, []driver.Value{historyCount}), nil
		case strings.Contains(q, "payload_hash = ?"):
			return &fakeRows{}, nil
		case strings.Contains(q, "status IN ('pending','sent')"):
			return fakeRowsSet([]string{"COUNT(*)"}, []driver.Value{int64(0)}), nil
		}
		return &fakeRows{}, nil
	}
	var inserted []fakeSQLCall
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO agent_tasks") {
			inserted = append(inserted, fakeSQLCall{Query: q, Args: fakeArgsAny(args)})
			return fakeResult(1, 99), nil
		}
		return fakeResult(1, 0), nil
	}

	ctx := context.Background()
	h.ensureAgentTokenFixTask(ctx, srv)
	if len(inserted) != 1 {
		t.Fatalf("应排 1 条自愈任务，实际 %d", len(inserted))
	}
	payload := ""
	if len(inserted[0].Args) > 3 {
		if s, ok := inserted[0].Args[3].(string); ok {
			payload = s
		}
	}
	for _, want := range []string{`"system_action":"agent_token_fix"`, "PROXYPILOT_TOKEN", "systemctl --no-block restart proxypilot-agent", ".bak."} {
		if !strings.Contains(payload, want) {
			t.Fatalf("自愈任务 payload 缺少 %q：%s", want, payload)
		}
	}
	if !strings.Contains(payload, token) {
		t.Fatal("自愈任务必须携带解出来的当前正确令牌（否则无法改写 conf）")
	}

	// 窗口内第二次 poll：已有历史任务 → 不再排
	historyCount = 1
	h.ensureAgentTokenFixTask(ctx, srv)
	if len(inserted) != 1 {
		t.Fatalf("窗口内重复调用不应再排任务，实际 %d 条", len(inserted))
	}
}

func TestAgentTokenFixTaskSkipsUndecryptableOrUnsafeToken(t *testing.T) {
	h, fdb := newGraceTestHandlers(t)
	var inserts int
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO agent_tasks") {
			inserts++
		}
		return fakeResult(1, 0), nil
	}
	ctx := context.Background()

	// 解密失败（历史 KEK 变更导致 enc:v1 解不开）：不能排任务，否则脚本里没有可用令牌
	h.ensureAgentTokenFixTask(ctx, &agentServer{ID: 9, GraceAuth: true, AgentTokenEnc: "enc:v1:%%%%"})
	if inserts != 0 {
		t.Fatalf("令牌解不开时不应排任务，实际 %d", inserts)
	}

	// 可疑字符：不允许进入 shell 命令
	h.ensureAgentTokenFixTask(ctx, &agentServer{ID: 10, GraceAuth: true, AgentTokenEnc: "ag_x'; reboot; #"})
	if inserts != 0 {
		t.Fatalf("可疑令牌不应排任务，实际 %d", inserts)
	}

	// 没有 grace 标记的普通请求不触发自愈
	h.ensureAgentTokenFixTask(ctx, &agentServer{ID: 11, AgentTokenEnc: "ag_" + strings.Repeat("cd", 32)})
	if inserts != 0 {
		t.Fatalf("非 grace 请求不应排自愈任务，实际 %d", inserts)
	}
}

func TestAgentTokenFixCommandIsIdempotentAndQuiet(t *testing.T) {
	token := "ag_" + strings.Repeat("ef", 32)
	cmd := agentTokenFixCommand(token)
	for _, want := range []string{
		"CONF=/etc/proxypilot-agent.conf",
		"TOKEN='" + token + "'",
		"tok_lines=$(grep -cE '^[[:space:]]*(PROXYPILOT_TOKEN|TOKEN|KEY)[[:space:]]*='",
		"grep -qE \"^PROXYPILOT_TOKEN=\\\"?$TOKEN\\\"?[[:space:]]*$\"",
		"token-unchanged",
		"token-rewritten",
		"printf 'PROXYPILOT_TOKEN=\"%s\"\\n' \"$TOKEN\"",
		"chmod 600",
		"cp -a \"$CONF\" \"$CONF.bak.",
		"systemctl --no-block restart proxypilot-agent",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("自愈脚本缺少 %q", want)
		}
	}
	if strings.Count(cmd, token) != 1 {
		t.Fatalf("令牌在脚本里出现 %d 次，应只有赋值那一次", strings.Count(cmd, token))
	}
	if strings.Contains(cmd, "set -x") {
		t.Fatal("脚本不能开启 set -x（会把令牌打印到任务输出）")
	}
}

func TestAgentTokenFixActionRegisteredAsSystemExec(t *testing.T) {
	if !isSystemExecPayload(map[string]any{"system_action": systemActionAgentTokenFix}) {
		t.Fatal("agent_token_fix 必须登记为系统级动作，否则 allow_exec=0 的节点无法执行自愈")
	}
	if systemActionAgentTokenFix != "agent_token_fix" {
		t.Fatalf("system_action 字面量被改动：%q", systemActionAgentTokenFix)
	}
}

func TestMigration0053AddsAgentAuthGrace(t *testing.T) {
	data, err := os.ReadFile("../../migrations/0053_agent_auth_grace.sql")
	if err != nil {
		t.Fatalf("读取 0053 迁移失败：%v", err)
	}
	body := string(data)
	for _, want := range []string{"agent_auth_grace", "ALTER TABLE `servers`", "TINYINT NOT NULL DEFAULT 0", "DO 0"} {
		if !strings.Contains(body, want) {
			t.Fatalf("0053 迁移缺少 %q", want)
		}
	}
}
