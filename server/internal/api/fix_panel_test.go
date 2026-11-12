package api

import (
	"context"
	"database/sql/driver"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
	"github.com/yzfn/proxypilot/server/internal/config"
)

// =====================================================================
// 面板侧修复的自证测试（对应 fix-panel-report.md 的 9 项）
// =====================================================================

// ---------- P1-1 批量任务 dedup_key 生命周期 ----------

func TestBatchJobDedupKeyStable(t *testing.T) {
	a := batchJobDedupKey("warp.update", `{"server_id":1}`)
	if a != batchJobDedupKey("warp.update", `{"server_id":1}`) {
		t.Fatal("同 kind+params 的 dedup_key 必须稳定")
	}
	if a == batchJobDedupKey("warp.update", `{"server_id":2}`) {
		t.Fatal("不同 params 的 dedup_key 必须不同")
	}
}

func TestJobsCancelClearsDedupKey(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COALESCE(status,'') FROM batch_jobs WHERE id = ?") {
			return fakeRowsSet([]string{"status"}, []driver.Value{"running"}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/9/cancel", nil)
	req.SetPathValue("id", "9")
	rec := httptest.NewRecorder()
	h.jobsCancel(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("取消任务返回 %d：%s", rec.Code, rec.Body.String())
	}
	call, ok := fdb.lastExec("UPDATE batch_jobs SET status = 'canceled'")
	if !ok {
		t.Fatal("没有执行取消 UPDATE")
	}
	if !strings.Contains(call.Query, "dedup_key = NULL") {
		t.Fatalf("取消任务的 SQL 没有清理 dedup_key：%s", call.Query)
	}
}

func TestRunBatchJobTerminalClearsDedupKey(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT * FROM batch_jobs WHERE id = ?") {
			// 未知任务类型：抢占成功后应立即置 failed 并清 dedup_key。
			return fakeRowSet([]string{"id", "kind", "params", "status", "runner_id"},
				map[string]any{"id": int64(7), "kind": "test.unknown_kind", "params": "{}", "status": "queued", "runner_id": ""}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.runBatchJobInline(context.Background(), 7, "tester")

	call, ok := fdb.lastExec("UPDATE batch_jobs SET status = ?, locked_at = 0")
	if !ok {
		t.Fatal("失败分支没有写终态")
	}
	if !strings.Contains(call.Query, "dedup_key = NULL") {
		t.Fatalf("失败终态没有清 dedup_key：%s", call.Query)
	}
	if len(call.Args) == 0 || call.Args[0] != "failed" {
		t.Fatalf("终态应为 failed，实际参数 %#v", call.Args)
	}
	if !strings.HasSuffix(strings.TrimSpace(call.Query), "runner_id = ?") {
		t.Fatalf("终态 UPDATE 必须带 runner_id 归属保护：%s", call.Query)
	}
}

// 历史脏数据：终态任务残留 dedup_key 时，新任务应换新键插入，而不是把旧任务当结果返回。
func TestBatchCreateJobReplacesStaleDedupKey(t *testing.T) {
	const kind = "test.stale_dedup"
	batchKinds[kind] = jobKind{
		Title:     func(_ map[string]any, n int) string { return "测试任务" },
		EmptyHint: "无目标",
		Items: func(_ *handlers, _ context.Context, _ map[string]any) ([]jobItem, error) {
			return []jobItem{{Target: "server", TargetID: 1, Label: "s1"}}, nil
		},
		Run: func(_ *handlers, _ context.Context, _ map[string]any, _ jobItem, _ string) (bool, string) {
			return true, ""
		},
	}
	t.Cleanup(func() { delete(batchKinds, kind) })

	fdb, db := newFakeDB(t)
	var insertJobs int64
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "INSERT INTO batch_jobs") {
			insertJobs++
			if insertJobs == 1 {
				// 第一次插入撞上「已结束任务的残留 dedup_key」
				return fakeResult(0, 0), nil
			}
			return fakeResult(1, 42), nil
		}
		return fakeResult(1, 0), nil
	}
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT id, total FROM batch_jobs WHERE kind = ?"):
			return &fakeRows{}, nil // 活跃任务预检：无重复
		case strings.Contains(q, "FROM batch_jobs WHERE dedup_key = ?"):
			// 残留键对应的旧任务已处于终态
			return fakeRowsSet([]string{"id", "total", "status"}, []driver.Value{int64(7), int64(3), "canceled"}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	jobID, total, duplicate, msg := h.batchCreateJob(context.Background(), kind, map[string]any{}, "tester")
	if msg != "" {
		t.Fatalf("创建失败：%s", msg)
	}
	if duplicate {
		t.Fatal("残留终态键不应被判定为重复任务")
	}
	if jobID != 42 || total != 1 {
		t.Fatalf("jobID=%d total=%d，期望 42/1", jobID, total)
	}
	if insertJobs != 2 {
		t.Fatalf("应重试插入一次（共 2 次），实际 %d", insertJobs)
	}
}

func TestBatchCreateJobActiveDuplicateReturned(t *testing.T) {
	const kind = "test.active_dup"
	batchKinds[kind] = jobKind{
		Title:     func(_ map[string]any, n int) string { return "测试任务" },
		EmptyHint: "无目标",
		Items: func(_ *handlers, _ context.Context, _ map[string]any) ([]jobItem, error) {
			return []jobItem{{Target: "server", TargetID: 1, Label: "s1"}}, nil
		},
		Run: func(_ *handlers, _ context.Context, _ map[string]any, _ jobItem, _ string) (bool, string) {
			return true, ""
		},
	}
	t.Cleanup(func() { delete(batchKinds, kind) })

	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT id, total FROM batch_jobs WHERE kind = ?") {
			return fakeRowsSet([]string{"id", "total"}, []driver.Value{int64(5), int64(2)}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	jobID, total, duplicate, msg := h.batchCreateJob(context.Background(), kind, map[string]any{}, "tester")
	if msg != "" || !duplicate || jobID != 5 || total != 2 {
		t.Fatalf("应返回活跃重复任务 5/2，实际 jobID=%d total=%d duplicate=%v msg=%q", jobID, total, duplicate, msg)
	}
}

// ---------- P1-2 locked_at 心跳续期 + 归属校验 ----------

func TestBatchJobHeartbeatKeepsLockAlive(t *testing.T) {
	oldInterval := batchJobHeartbeatInterval
	batchJobHeartbeatInterval = 30
	t.Cleanup(func() { batchJobHeartbeatInterval = oldInterval })

	const kind = "test.heartbeat"
	var runCount atomic.Int64
	batchKinds[kind] = jobKind{
		Title:     func(_ map[string]any, n int) string { return "心跳测试" },
		EmptyHint: "无目标",
		Items: func(_ *handlers, _ context.Context, _ map[string]any) ([]jobItem, error) {
			return []jobItem{{Target: "server", TargetID: 1, Label: "s1"}}, nil
		},
		Run: func(_ *handlers, _ context.Context, _ map[string]any, _ jobItem, _ string) (bool, string) {
			runCount.Add(1)
			// 模拟「单项执行时间超过锁阈值」：执行期间必须靠并发心跳续期
			time.Sleep(150 * time.Millisecond)
			return true, ""
		},
	}
	t.Cleanup(func() { delete(batchKinds, kind) })

	fdb, db := newFakeDB(t)
	var mu sync.Mutex
	claimedRunner := ""
	var heartbeatCount atomic.Int64
	var finishCall fakeSQLCall
	finishSeen := false
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		switch {
		case strings.Contains(q, "SET status = 'running'"):
			mu.Lock()
			if len(args) > 1 {
				if s, ok := args[1].(string); ok {
					claimedRunner = s
				}
			}
			mu.Unlock()
			return fakeResult(1, 0), nil
		case strings.Contains(q, "dedup_key = NULL"):
			mu.Lock()
			finishCall = fakeSQLCall{Query: q, Args: fakeArgsAny(args)}
			finishSeen = true
			mu.Unlock()
			return fakeResult(1, 0), nil
		case strings.Contains(q, "SET locked_at = ?"):
			heartbeatCount.Add(1)
			return fakeResult(1, 0), nil
		}
		return fakeResult(1, 0), nil
	}
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT * FROM batch_jobs WHERE id = ?"):
			return fakeRowSet([]string{"id", "kind", "params", "status", "runner_id"},
				map[string]any{"id": int64(7), "kind": kind, "params": "{}", "status": "queued", "runner_id": ""}), nil
		case strings.Contains(q, "SELECT * FROM batch_job_items"):
			return fakeRowsSet([]string{"id", "seq", "target", "target_id", "label"},
				[]driver.Value{int64(1), int64(1), "server", int64(1), "s1"}), nil
		case strings.Contains(q, "SELECT COALESCE(status,''), COALESCE(runner_id,'') FROM batch_jobs"):
			mu.Lock()
			r := claimedRunner
			mu.Unlock()
			return fakeRowsSet([]string{"status", "runner_id"}, []driver.Value{"running", r}), nil
		}
		return &fakeRows{}, nil
	}

	h := &handlers{db: db, log: testLogger()}
	h.runBatchJobInline(context.Background(), 7, "tester")

	if runCount.Load() != 1 {
		t.Fatalf("任务项应执行 1 次，实际 %d", runCount.Load())
	}
	if heartbeatCount.Load() < 2 {
		t.Fatalf("执行期间应至少续期 2 次 locked_at，实际 %d（单项耗时 150ms，心跳 30ms）", heartbeatCount.Load())
	}
	mu.Lock()
	runner := claimedRunner
	call := finishCall
	seen := finishSeen
	mu.Unlock()
	if runner == "" || runner == "go" {
		t.Fatalf("runner_id 必须是本次执行唯一标识，实际 %q", runner)
	}
	if !seen {
		t.Fatal("任务结束后没有写终态")
	}
	if len(call.Args) < 5 || call.Args[4] != runner {
		t.Fatalf("终态 UPDATE 的 runner_id 参数应为 %q，实际 %#v", runner, call.Args)
	}
}

// 任务被取消 / 被 cron 接管后，执行器必须停止，不能继续跑剩余项。
func TestBatchJobStopsWhenLockLost(t *testing.T) {
	const kind = "test.lock_lost"
	var runCount atomic.Int64
	batchKinds[kind] = jobKind{
		Title:     func(_ map[string]any, n int) string { return "归属测试" },
		EmptyHint: "无目标",
		Items: func(_ *handlers, _ context.Context, _ map[string]any) ([]jobItem, error) {
			return []jobItem{{Target: "server", TargetID: 1, Label: "s1"}}, nil
		},
		Run: func(_ *handlers, _ context.Context, _ map[string]any, _ jobItem, _ string) (bool, string) {
			runCount.Add(1)
			return true, ""
		},
	}
	t.Cleanup(func() { delete(batchKinds, kind) })

	fdb, db := newFakeDB(t)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT * FROM batch_jobs WHERE id = ?"):
			return fakeRowSet([]string{"id", "kind", "params", "status", "runner_id"},
				map[string]any{"id": int64(8), "kind": kind, "params": "{}", "status": "queued", "runner_id": ""}), nil
		case strings.Contains(q, "SELECT * FROM batch_job_items"):
			return fakeRowsSet([]string{"id", "seq", "target", "target_id", "label"},
				[]driver.Value{int64(1), int64(1), "server", int64(1), "s1"},
				[]driver.Value{int64(2), int64(2), "server", int64(2), "s2"}), nil
		case strings.Contains(q, "SELECT COALESCE(status,''), COALESCE(runner_id,'') FROM batch_jobs"):
			// 第一个项执行前锁就被取消
			return fakeRowsSet([]string{"status", "runner_id"}, []driver.Value{"canceled", ""}), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.runBatchJobInline(context.Background(), 8, "tester")
	if runCount.Load() != 0 {
		t.Fatalf("失去锁后不应再执行任务项，实际执行 %d 次", runCount.Load())
	}
	if fdb.countExec("dedup_key = NULL") != 0 {
		t.Fatal("失去锁后不应写终态（避免覆盖取消状态）")
	}
}

func TestCronBatchTakeoverUsesHeartbeatTimeout(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	now := int64(1_700_000_000_000)
	h.cronTaskMaintenance(context.Background(), now)
	call, ok := fdb.lastExec("SET locked_at = 0, runner_id = '', status = 'queued'")
	if !ok {
		t.Fatal("没有执行批量任务锁回收")
	}
	if len(call.Args) != 2 {
		t.Fatalf("参数数量 %d，期望 2", len(call.Args))
	}
	got, _ := call.Args[1].(int64)
	want := now - batchJobLockTimeoutMS
	if got != want {
		t.Fatalf("锁超时阈值 = %d，期望 %d（= now - batchJobLockTimeoutMS）", got, want)
	}
	if batchJobHeartbeatIntervalMS >= batchJobLockTimeoutMS {
		t.Fatalf("心跳间隔 %d 必须显著小于锁阈值 %d", batchJobHeartbeatIntervalMS, batchJobLockTimeoutMS)
	}
}

// ---------- P2-1 agent_tasks 清理状态集合 ----------

func TestAgentTasksCleanupSQLCoversDoneAndSkipped(t *testing.T) {
	for _, status := range []string{"done", "success", "failed", "expired", "skipped"} {
		if !strings.Contains(agentTasksCleanupSQL, "'"+status+"'") {
			t.Fatalf("清理 SQL 缺少终态 %q：%s", status, agentTasksCleanupSQL)
		}
	}
	if !strings.Contains(agentTasksCleanupSQL, "finished_at < ?") {
		t.Fatalf("清理 SQL 必须按 finished_at 判定保留期：%s", agentTasksCleanupSQL)
	}
	// 旧版本 ack 不给 failed/skipped 写 finished_at，历史残留行必须能用 created_at 兜底清理。
	if !strings.Contains(agentTasksCleanupSQL, "created_at > 0 AND created_at < ?") {
		t.Fatalf("清理 SQL 需要对 finished_at=0 的历史终态用 created_at 兜底：%s", agentTasksCleanupSQL)
	}
}

// ---------- P1-3 content_hash 口径 ----------

func TestNodeConfigContentHashIsRawButDeployShaIsRendered(t *testing.T) {
	_, db := newFakeDB(t) // node_templates 查询返回空，模板视为不存在
	h := &handlers{db: db, log: testLogger()}

	raw := `{"listen":"{{SERVER_HOST}}:443"}`
	if got := nodeConfigContentHash(raw); got != sha256Hex(raw) {
		t.Fatalf("content_hash 必须是原文哈希：%s != sha256Hex(content)", got)
	}
	cfg := &nodeDeployConfig{ID: 1, Name: "raw-hash-test", Content: raw, Engine: "other"}
	srv := &agentServer{ID: 2, Name: "node1", Host: "node1.example.com"}
	payload := h.agentBuildDeployPayload(context.Background(), cfg, srv)
	rendered := mapStr(payload, "content", "")
	if rendered == raw || !strings.Contains(rendered, "node1.example.com:443") {
		t.Fatalf("下发内容必须经过占位符渲染，实际 %q", rendered)
	}
	deploySha := mapStr(payload, "sha256", "")
	if deploySha != sha256Hex(rendered) {
		t.Fatalf("部署 sha256 必须是渲染后内容的哈希：%s != %s", deploySha, sha256Hex(rendered))
	}
	if nodeConfigContentHash(raw) == deploySha {
		t.Fatal("占位符配置的原文哈希与下发内容哈希相同，说明口径又被混淆了（自愈会误判重下发）")
	}
}

// ---------- P1-4 system_action 白名单 ----------

func TestIsSystemExecPayloadIncludesWarpUpdate(t *testing.T) {
	for _, action := range systemExecActions {
		if !isSystemExecPayload(map[string]any{"system_action": action}) {
			t.Fatalf("已登记的动作 %q 未被识别", action)
		}
	}
	if !isSystemExecPayload(map[string]any{"system_action": "warp_update"}) {
		t.Fatal("warp_update 必须在系统级动作白名单里（否则 allow_exec=0 的机器必然失败）")
	}
	if isSystemExecPayload(map[string]any{"system_action": "rm_rf"}) {
		t.Fatal("任意 system_action 不应通过白名单")
	}
	if isSystemExecPayload(map[string]any{}) {
		t.Fatal("没有 system_action 的 exec 不应通过白名单")
	}
}

// 防再次漏配：本包源码里出现的每一个 "system_action": "<字面量>" 都必须已登记。
func TestAllSystemActionLiteralsRegistered(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("读取源码失败：%v", err)
	}
	re := regexp.MustCompile(`"system_action":\s*"([a-z0-9_]+)"`)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(string(data), -1) {
			checked++
			if !isSystemExecPayload(map[string]any{"system_action": m[1]}) {
				t.Errorf("%s 里的 system_action=%q 没有登记到 systemExecActions（allow_exec=0 的机器会失败）", f, m[1])
			}
		}
	}
	if !strings.Contains(strings.Join(systemExecActions, ","), "warp_update") {
		t.Fatalf("systemExecActions 缺少 warp_update：%#v", systemExecActions)
	}
	t.Logf("扫描到 %d 处 system_action 字面量，全部已登记", checked)
}

func TestSensitiveSettingChangedNormalizesValues(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM app_settings") {
			return fakeRowsSet([]string{"key", "value"},
				[]driver.Value{"agent_access_urls", `["http://10.99.0.1:8080"]`},
				[]driver.Value{"auth_password_enabled", "1"},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	ctx := context.Background()

	// 设置页会重新序列化 JSON：格式差异不算改动
	if h.sensitiveSettingChanged(ctx, settingAgentAccessURLs, `[ "http://10.99.0.1:8080" ]`) {
		t.Fatal("仅格式不同的 agent_access_urls 不应判为已修改")
	}
	if !h.sensitiveSettingChanged(ctx, settingAgentAccessURLs, `["http://evil.example"]`) {
		t.Fatal("真正改了 agent_access_urls 必须判为已修改")
	}
	// 布尔项按 0/1 语义比较
	if h.sensitiveSettingChanged(ctx, settingPasswordEnabled, "1") {
		t.Fatal("布尔值未变化不应判为已修改")
	}
	if !h.sensitiveSettingChanged(ctx, settingPasswordEnabled, "0") {
		t.Fatal("关闭密码登录必须判为已修改")
	}
}

// ---------- P1-5 证书下载鉴权 ----------

// 证书下载接口只认 Authorization 头：带 Header → 200；只在 query 里放 token → 401。
// 这正是前端必须改用 fetch + Header 的原因（旧实现 window.open 不会带 Header）。
func TestVpnCertDownloadOnlyAcceptsAuthorizationHeader(t *testing.T) {
	const secret = "test-secret-cert-download"
	const superAdmin = "admin"
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM vpn_certificates WHERE id = 1") {
			return fakeRowsSet(
				[]string{"domains", "source", "cert_pem", "key_pem", "chain_pem", "fingerprint", "not_before", "not_after", "updated_at", "key_pem_enc"},
				[]driver.Value{"*.example.com", "acme", "CERT-PEM", "KEY-PEM", "CHAIN-PEM", "fp", int64(1), int64(2), int64(3), ""},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{cfg: config.Config{JWTSecret: secret, SuperAdmin: superAdmin}, db: db, log: testLogger()}
	token, err := auth.Sign(auth.Claims{Sub: superAdmin, Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}, secret)
	if err != nil {
		t.Fatalf("签发令牌失败：%v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/vpn/cert/download", h.authed(authOpts{}, h.vpnCertDownload))

	// 1) Authorization 头 → 200，返回私钥内容
	req := httptest.NewRequest(http.MethodGet, "/api/vpn/cert/download?kind=key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("带 Authorization 头下载 = %d，期望 200：%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "KEY-PEM" {
		t.Fatalf("下载内容 = %q，期望 KEY-PEM", rec.Body.String())
	}
	if !strings.Contains(rec.Header().Get("Content-Disposition"), "site.key") {
		t.Fatalf("Content-Disposition = %q", rec.Header().Get("Content-Disposition"))
	}

	// 2) 只把 token 放 query（旧前端做法）→ 401，证明确实只认 Header
	req2 := httptest.NewRequest(http.MethodGet, "/api/vpn/cert/download?kind=key&token="+url.QueryEscape(token), nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("仅 query token 下载 = %d，期望 401（接口只认 Authorization 头）", rec2.Code)
	}
}

// ---------- P2-3 / P2-4 表名 / 列名修正 ----------

func TestIssuedNodeCertsSQLUsesRealTable(t *testing.T) {
	if strings.Contains(issuedNodeCertsSQL, "vpn_node_certs") {
		t.Fatalf("仍在引用不存在的表 vpn_node_certs：%s", issuedNodeCertsSQL)
	}
	if !strings.Contains(issuedNodeCertsSQL, "FROM vpn_certs") {
		t.Fatalf("应统计真实表 vpn_certs：%s", issuedNodeCertsSQL)
	}
	if !migrationDeclaresTable(t, "vpn_certs") {
		t.Fatal("仓库迁移里找不到 vpn_certs 表")
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		return fakeRowsSet([]string{"COUNT(*)"}, []driver.Value{int64(5)}), nil
	}
	h := &handlers{db: db, log: testLogger()}
	n, err := h.countIssuedNodeCerts(context.Background())
	if err != nil || n != 5 {
		t.Fatalf("countIssuedNodeCerts() = %d, %v；期望 5, nil", n, err)
	}
}

func TestLocalServerIP4UsesRealColumn(t *testing.T) {
	if strings.Contains(localServerIP4SQL, "last_ip4") {
		t.Fatalf("仍在使用不存在的列 local_server.last_ip4：%s", localServerIP4SQL)
	}
	if !strings.Contains(localServerIP4SQL, "SELECT ip4 FROM local_server") {
		t.Fatalf("应从 local_server.ip4 读取：%s", localServerIP4SQL)
	}
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		return fakeRowsSet([]string{"ip4"}, []driver.Value{"203.0.113.9"}), nil
	}
	h := &handlers{db: db, log: testLogger()}
	if got := h.localServerIP4(context.Background()); got != "203.0.113.9" {
		t.Fatalf("localServerIP4() = %q，期望 203.0.113.9", got)
	}
}

// ---------- P2-9 serverDelete 级联 ----------

func TestServerDeleteCascadeCoverage(t *testing.T) {
	joined := strings.Join(serverDeleteCascadeSQL, "\n")
	wantTables := []string{
		"configs", "singbox_nodes", "mihomo_nodes", "mtproto_nodes", "mtproto_clients",
		"vpn_nodes", "vpn_node_grants", "vpn_sessions", "vpn_node_links", "vpn_certs",
		"node_configs", "server_metrics", "node_push_logs", "agent_tasks",
		"server_traffic_limits", "server_traffic_usage", "server_unlock_checks",
		"shell_sessions", "uuid_port_map", "vpn_mesh_nodes",
		"node_connections", "node_stats_snapshot", "node_user_counter", "user_node_recent",
		"mihomo_traffic_hourly", "user_traffic_hourly", "servers",
	}
	for _, tbl := range wantTables {
		if !regexp.MustCompile(`\b` + regexp.QuoteMeta(tbl) + `\b`).MatchString(joined) {
			t.Errorf("级联清理漏掉表 %s", tbl)
		}
		if !migrationDeclaresTable(t, tbl) {
			t.Errorf("级联里的表 %s 在仓库迁移里不存在（表名写错？）", tbl)
		}
	}

	// 顺序：子表先于父表，servers 最后。
	idx := func(substr string) int {
		for i, q := range serverDeleteCascadeSQL {
			if strings.Contains(q, substr) {
				return i
			}
		}
		return -1
	}
	order := []struct{ child, parent string }{
		{"mtproto_clients", "DELETE FROM mtproto_nodes"},
		{"vpn_certs", "DELETE FROM vpn_nodes"},
		{"vpn_node_links", "DELETE FROM vpn_nodes"},
		{"node_connections", "DELETE FROM singbox_nodes"},
		{"mihomo_traffic_hourly", "DELETE FROM mihomo_nodes"},
		{"user_traffic_hourly", "DELETE FROM singbox_nodes"},
	}
	for _, o := range order {
		c, p := idx(o.child), idx(o.parent)
		if c < 0 || p < 0 {
			t.Errorf("找不到 %s / %s 的删除语句", o.child, o.parent)
			continue
		}
		if c >= p {
			t.Errorf("顺序错误：%s 必须在 %s 之前删除", o.child, o.parent)
		}
	}
	last := serverDeleteCascadeSQL[len(serverDeleteCascadeSQL)-1]
	if !strings.Contains(last, "DELETE FROM servers") {
		t.Fatalf("最后一条必须是删除 servers，实际：%s", last)
	}
}

// ---------- P2-14 安全敏感设置仅超管可改 ----------

func TestSuperOnlySettingsClassification(t *testing.T) {
	for _, key := range []string{
		settingAgentBinaryBase, settingComponentBase, settingComponentVersion,
		settingAgentAccessURLs, settingAgentMeshCenter,
		settingPasswordEnabled, settingOIDCEnabled, settingOIDCIssuer, settingOIDCSecret,
		settingTurnstileSecret, settingSMTPPass, settingCFToken, settingR2SecretKey,
		settingPublicBaseURL,
	} {
		if !isSuperOnlySetting(key) {
			t.Errorf("%s 应归类为超级管理员专属设置", key)
		}
	}
	for _, key := range []string{"monitor_notify_emails", settingSMTPAddr, settingMailFrom, "batch_retention_days"} {
		if isSuperOnlySetting(key) {
			t.Errorf("%s 不应被限制（普通管理员需要维护通知 / 邮件 / 保留期）", key)
		}
	}
}

func TestSensitiveSettingsRequireSuperAdmin(t *testing.T) {
	const secret = "test-secret-settings-gate"
	const superAdmin = "admin"
	_, db := newFakeDB(t)
	h := &handlers{
		cfg: config.Config{JWTSecret: secret, SuperAdmin: superAdmin},
		db:  db, log: testLogger(),
	}
	ordinary, err := auth.Sign(auth.Claims{Sub: "shao", Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}, secret)
	if err != nil {
		t.Fatalf("签发普通管理员令牌失败：%v", err)
	}
	super, err := auth.Sign(auth.Claims{Sub: superAdmin, Role: "admin", Typ: auth.TokenTypeAccess, Exp: time.Now().Add(time.Hour).UnixMilli()}, secret)
	if err != nil {
		t.Fatalf("签发超管令牌失败：%v", err)
	}
	call := func(token, body string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.authed(authOpts{}, h.settingsAPI)(rec, req)
		return rec.Code
	}

	// 1) 普通管理员改分发基址（提权链）→ 403
	if got := call(ordinary, `{"agent_binary_base_url":"https://evil.example/agent"}`); got != http.StatusForbidden {
		t.Fatalf("普通管理员改 Agent 分发基址 = %d，期望 403", got)
	}
	// 2) 普通管理员改登录方式 → 403
	if got := call(ordinary, `{"auth_password_enabled":"0"}`); got != http.StatusForbidden {
		t.Fatalf("普通管理员关闭密码登录 = %d，期望 403", got)
	}
	// 3) 普通管理员原样保存（敏感项未改动）+ 改普通设置 → 200，不能被误伤
	if got := call(ordinary, `{"agent_binary_base_url":"","monitor_notify_emails":"ops@example.com"}`); got != http.StatusOK {
		t.Fatalf("普通管理员保存普通设置 = %d，期望 200（整表单回传不应被误杀）", got)
	}
	// 4) 超管可以改敏感项
	if got := call(super, `{"agent_binary_base_url":"https://cdn.example.com/agent-dist"}`); got != http.StatusOK {
		t.Fatalf("超管改 Agent 分发基址 = %d，期望 200", got)
	}
	// 5) 超管也不能把分发基址写成非 http(s)
	if got := call(super, `{"agent_binary_base_url":"file:///etc/passwd"}`); got != http.StatusBadRequest {
		t.Fatalf("非法分发基址 = %d，期望 400", got)
	}
}

// migrationDeclaresTable 判断仓库 migrations 里是否声明了某张表。
func migrationDeclaresTable(t *testing.T, table string) bool {
	t.Helper()
	files, err := filepath.Glob("../../migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("找不到 migrations 目录：%v", err)
	}
	re := regexp.MustCompile(`(?i)CREATE TABLE( IF NOT EXISTS)?\s+` + "`?" + regexp.QuoteMeta(table) + "`?[\\s(]")
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if re.Match(data) {
			return true
		}
	}
	return false
}
