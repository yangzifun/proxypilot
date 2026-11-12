package api

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

var errFakeDuplicate = errors.New("Error 1062 (23000): Duplicate entry '7' for key 'PRIMARY'")

// =====================================================================
// task-17：节点累计流量单调累计（sing-box 重启后不回退）
// =====================================================================

func TestLifetimeDelta(t *testing.T) {
	cases := []struct {
		name            string
		raw, last, want int64
	}{
		{"正常递增", 150, 100, 50},
		{"相等（重复上报）", 100, 100, 0},
		{"重启归零（raw 变小）", 30, 100, 30},
		{"重启后第一轮为 0", 0, 100, 0},
		{"首次上报", 70, 0, 70},
		{"负数防御", -5, 100, 0},
		{"负数 last", 10, -1, 10},
	}
	for _, tc := range cases {
		if got := lifetimeDelta(tc.raw, tc.last); got != tc.want {
			t.Fatalf("%s：lifetimeDelta(%d,%d) = %d，期望 %d", tc.name, tc.raw, tc.last, got, tc.want)
		}
	}
}

// lifetimeDelta 是纯函数：并发调用无数据竞争且结果稳定。
func TestLifetimeDeltaConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := lifetimeDelta(30, 100); got != 30 {
				t.Errorf("重启场景 delta = %d，期望 30", got)
			}
			if got := lifetimeDelta(150, 100); got != 50 {
				t.Errorf("递增场景 delta = %d，期望 50", got)
			}
		}()
	}
	wg.Wait()
}

// lifetimeFakeState 是有状态的 fake 表 node_traffic_lifetime。
type lifetimeFakeState struct {
	mu                         sync.Mutex
	exists                     bool
	up, down, lastUp, lastDown int64
}

// newLifetimeFake 返回 fake DB + 可查询/更新状态的 *sql.DB。
func newLifetimeFake(t *testing.T) (*fakeDB, *sql.DB, *lifetimeFakeState) {
	t.Helper()
	fdb, db := newFakeDB(t)
	st := &lifetimeFakeState{}
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		if strings.Contains(q, "FROM node_traffic_lifetime") {
			if !st.exists {
				return &fakeRows{}, nil // 扫描即 sql.ErrNoRows
			}
			return fakeRowsSet([]string{"up_total", "down_total", "last_raw_up", "last_raw_down"},
				[]driver.Value{st.up, st.down, st.lastUp, st.lastDown}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		st.mu.Lock()
		defer st.mu.Unlock()
		arg := func(i int) int64 {
			if i >= len(args) {
				return 0
			}
			if v, ok := args[i].(int64); ok {
				return v
			}
			return 0
		}
		switch {
		case strings.Contains(q, "INSERT INTO node_traffic_lifetime"):
			// node_id, up_total, down_total, last_raw_up, last_raw_down, updated_at
			st.exists = true
			st.up, st.down, st.lastUp, st.lastDown = arg(1), arg(2), arg(3), arg(4)
		case strings.Contains(q, "UPDATE node_traffic_lifetime"):
			// up_total, down_total, last_raw_up, last_raw_down, updated_at, node_id
			st.up, st.down, st.lastUp, st.lastDown = arg(0), arg(1), arg(2), arg(3)
		}
		return fakeResult(1, 0), nil
	}
	return fdb, db, st
}

func TestAccumulateNodeLifetimeNormalIncrement(t *testing.T) {
	_, db, st := newLifetimeFake(t)
	st.exists, st.up, st.down, st.lastUp, st.lastDown = true, 1000, 500, 100, 50
	h := &handlers{db: db, log: testLogger()}
	if err := h.accumulateNodeLifetime(context.Background(), 7, 150, 80, 100, 50, 1000); err != nil {
		t.Fatalf("accumulate 失败：%v", err)
	}
	if st.up != 1050 || st.down != 530 || st.lastUp != 150 || st.lastDown != 80 {
		t.Fatalf("累计结果 = up=%d down=%d last=%d/%d，期望 1050/530/150/80",
			st.up, st.down, st.lastUp, st.lastDown)
	}
}

func TestAccumulateNodeLifetimeAfterRestart(t *testing.T) {
	_, db, st := newLifetimeFake(t)
	st.exists, st.up, st.down, st.lastUp, st.lastDown = true, 1000, 500, 100, 50
	h := &handlers{db: db, log: testLogger()}
	// sing-box 重启：raw 从 100 掉到 20 → 应继续 +20，而不是回退或清零
	if err := h.accumulateNodeLifetime(context.Background(), 7, 20, 10, 100, 50, 2000); err != nil {
		t.Fatalf("accumulate 失败：%v", err)
	}
	if st.up != 1020 || st.down != 510 || st.lastUp != 20 || st.lastDown != 10 {
		t.Fatalf("重启后 = up=%d down=%d last=%d/%d，期望 1020/510/20/10",
			st.up, st.down, st.lastUp, st.lastDown)
	}
	// 重启后继续增长：20 → 60，再 +40
	if err := h.accumulateNodeLifetime(context.Background(), 7, 60, 30, 20, 10, 3000); err != nil {
		t.Fatalf("accumulate 失败：%v", err)
	}
	if st.up != 1060 || st.down != 530 {
		t.Fatalf("重启后继续累加 = up=%d down=%d，期望 1060/530", st.up, st.down)
	}
}

func TestAccumulateNodeLifetimeFirstReportUsesSnapshotBaseline(t *testing.T) {
	_, db, st := newLifetimeFake(t)
	h := &handlers{db: db, log: testLogger()}
	// 首次见到节点：lifetime 初值 = 旧快照累计 900/400，last_raw = 当前 raw 950/410
	if err := h.accumulateNodeLifetime(context.Background(), 7, 950, 410, 900, 400, 1000); err != nil {
		t.Fatalf("accumulate 失败：%v", err)
	}
	if st.up != 900 || st.down != 400 || st.lastUp != 950 || st.lastDown != 410 {
		t.Fatalf("首轮初始化 = up=%d down=%d last=%d/%d，期望 900/400/950/410",
			st.up, st.down, st.lastUp, st.lastDown)
	}
	// 下一轮 1010/430 → 960/420
	if err := h.accumulateNodeLifetime(context.Background(), 7, 1010, 430, 950, 410, 2000); err != nil {
		t.Fatalf("accumulate 失败：%v", err)
	}
	if st.up != 960 || st.down != 420 {
		t.Fatalf("首轮后的累加 = up=%d down=%d，期望 960/420", st.up, st.down)
	}
}

// 重复上报同一份 raw：不得重复计数。
func TestAccumulateNodeLifetimeDuplicateReports(t *testing.T) {
	_, db, st := newLifetimeFake(t)
	h := &handlers{db: db, log: testLogger()}
	st.exists, st.up, st.down, st.lastUp, st.lastDown = true, 500, 300, 100, 60
	for i := 0; i < 5; i++ {
		if err := h.accumulateNodeLifetime(context.Background(), 7, 100, 60, 100, 60, int64(1000+i*30)); err != nil {
			t.Fatalf("accumulate 失败：%v", err)
		}
	}
	if st.up != 500 || st.down != 300 {
		t.Fatalf("重复上报后累计不应变化：up=%d down=%d", st.up, st.down)
	}
}

// 跨小时点：累计只由 raw 差决定，与采样时间/整点边界无关。
func TestAccumulateNodeLifetimeAcrossHourBoundary(t *testing.T) {
	_, db, st := newLifetimeFake(t)
	h := &handlers{db: db, log: testLogger()}
	const hour = int64(3_600_000)
	base := int64(10) * hour // 10:00 整点
	steps := []struct {
		now      int64
		up, down int64
		wantUp   int64
		wantDown int64
	}{
		{base + 59*60_000, 100, 10, 0, 0},     // 首次上报：lifetime 从 0 起，last_raw=100/10
		{base + 60*60_000, 300, 30, 200, 20},  // 11:00 跨整点：+200/+20
		{base + 121*60_000, 400, 40, 300, 30}, // 12:01：+100/+10
		{base + 122*60_000, 50, 5, 350, 35},   // 重启归零：+50/+5（不回退）
		{base + 123*60_000, 80, 8, 380, 38},   // 重启后继续增长：+30/+3
	}
	for _, s := range steps {
		if err := h.accumulateNodeLifetime(context.Background(), 9, s.up, s.down, 0, 0, s.now); err != nil {
			t.Fatalf("accumulate 失败：%v", err)
		}
		if st.up != s.wantUp || st.down != s.wantDown {
			t.Fatalf("t=%d 累计 = up=%d down=%d，期望 %d/%d", s.now, st.up, st.down, s.wantUp, s.wantDown)
		}
	}
}

// ingestSnapshot 应同时写 lifetime（单调累计）与 snapshot（原始计数）。
func TestIngestSnapshotWritesLifetimeAndSnapshot(t *testing.T) {
	fdb, db, st := newLifetimeFake(t)
	st.exists, st.up, st.down, st.lastUp, st.lastDown = true, 1000, 500, 100, 50
	baseQuery := fdb.onQuery
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM node_stats_snapshot") {
			// 上一轮快照：原始 100/50，30s 前
			return fakeRowsSet([]string{"sampled_at", "up_total", "down_total"},
				[]driver.Value{int64(90_000), int64(100), int64(50)}), nil
		}
		return baseQuery(q, args)
	}
	h := &handlers{db: db, log: testLogger()}
	h.ingestSnapshot(context.Background(), 7, 150, 80, 3, 120_000)
	if st.up != 1050 || st.down != 530 {
		t.Fatalf("lifetime 未更新：up=%d down=%d，期望 1050/530", st.up, st.down)
	}
	call, ok := fdb.lastExec("INSERT INTO node_stats_snapshot")
	if !ok {
		t.Fatal("未写入原始快照")
	}
	// snapshot 仍保存原始计数（重启识别依赖它）
	if up, _ := call.Args[3].(int64); up != 150 {
		t.Fatalf("snapshot up_total = %v，期望原始值 150", call.Args[3])
	}
	if down, _ := call.Args[4].(int64); down != 80 {
		t.Fatalf("snapshot down_total = %v，期望原始值 80", call.Args[4])
	}
}

// 并发首轮上报：两个请求同时发现没有 lifetime 行，其中一个插入撞主键 →
// 自动重试走增量更新，不得重复计数或报错。
func TestAccumulateNodeLifetimeConcurrentInsertRetry(t *testing.T) {
	fdb, db := newFakeDB(t)
	var exists bool
	var up, down, lastUp, lastDown int64
	insertAttempts, updateCalls := 0, 0
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM node_traffic_lifetime") {
			if !exists {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"up_total", "down_total", "last_raw_up", "last_raw_down"},
				[]driver.Value{up, down, lastUp, lastDown}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		arg := func(i int) int64 {
			if i < len(args) {
				if v, ok := args[i].(int64); ok {
					return v
				}
			}
			return 0
		}
		switch {
		case strings.Contains(q, "INSERT INTO node_traffic_lifetime"):
			insertAttempts++
			if insertAttempts == 1 {
				// 模拟另一个并发请求已经插入：本次 INSERT 撞主键
				exists = true
				up, down, lastUp, lastDown = 900, 400, 950, 410
				return nil, errFakeDuplicate
			}
			exists = true
			up, down, lastUp, lastDown = arg(1), arg(2), arg(3), arg(4)
		case strings.Contains(q, "UPDATE node_traffic_lifetime"):
			updateCalls++
			up, down, lastUp, lastDown = arg(0), arg(1), arg(2), arg(3)
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	if err := h.accumulateNodeLifetime(context.Background(), 7, 950, 410, 900, 400, 1000); err != nil {
		t.Fatalf("并发插入重试失败：%v", err)
	}
	if insertAttempts != 1 || updateCalls != 1 {
		t.Fatalf("并发插入重试路径异常：insert=%d update=%d", insertAttempts, updateCalls)
	}
	if up != 900 || down != 400 {
		t.Fatalf("并发重试后不应重复计数：up=%d down=%d，期望 900/400", up, down)
	}
}

// API 返回 lifetime（历史求和），而不是 sing-box 原始计数。
func TestStatsTrafficNodesReturnsLifetime(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM singbox_nodes") {
			return fakeRowsSet(
				[]string{"node_id", "node_name", "server_id", "server_name", "stats_enabled",
					"sampled_at", "conn_count", "up_total", "down_total", "up_rate", "down_rate", "stats_error"},
				[]driver.Value{int64(1), "n1", int64(10), "s1", int64(1),
					int64(1_000), int64(2), int64(5000), int64(6000), int64(10), int64(20), ""},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rec := httptest.NewRecorder()
	h.statsTrafficNodes(rec, httptest.NewRequest(http.MethodGet, "/api/stats/traffic/nodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d：%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析响应失败：%v", err)
	}
	if len(out.Data) != 1 {
		t.Fatalf("data = %#v", out.Data)
	}
	if got := out.Data[0]["up_total"]; got != float64(5000) {
		t.Fatalf("up_total = %v，期望 lifetime 5000", got)
	}
	if got := out.Data[0]["down_total"]; got != float64(6000) {
		t.Fatalf("down_total = %v，期望 lifetime 6000", got)
	}
	// SQL 必须 LEFT JOIN node_traffic_lifetime（读历史累计而不是 raw）
	calls := fdb.queryCalls()
	if len(calls) == 0 || !strings.Contains(calls[0].Query, "node_traffic_lifetime") {
		t.Fatalf("统计接口未读取 lifetime 表：%#v", calls)
	}
}