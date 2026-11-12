package api

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// errFakeUnavailable 模拟统计表/查询不可用。
var errFakeUnavailable = errors.New("fake db unavailable")

// =====================================================================
// V1.3 统计口径修正：「节点连接数 0 / 节点实时 0」的修复单测
//
// 口径：
//   - 在线用户 = 近 5 分钟在该节点有流量增量的去重用户数（user_node_recent）
//   - 节点实时 = 近 5 分钟窗口平均速率（真实原始计数首尾差 / 实际跨度）
//   - 瞬时连接数（conn_count）与单次采样速率（up_rate/down_rate）仍然原样返回
// =====================================================================

func TestNodeWindowSummary(t *testing.T) {
	samples := []nodeRateSample{
		{at: 0, up: 1000, down: 2000, connCount: 1},
		{at: 60_000, up: 4000, down: 5000, connCount: 3},
		{at: 120_000, up: 7000, down: 8000, connCount: 5},
	}
	up, down, span, peak := nodeWindowSummary(samples, 7, 8)
	if up != 50 || down != 50 {
		t.Fatalf("窗口平均速率 = %d/%d，期望 50/50", up, down)
	}
	if span != 120_000 {
		t.Fatalf("span = %d，期望 120000", span)
	}
	if peak != 5 {
		t.Fatalf("连接峰值 = %d，期望 5", peak)
	}

	// 不足两个样本 → 回退单次采样速率（不伪造平均）
	up, down, span, peak = nodeWindowSummary(samples[:1], 7, 8)
	if up != 7 || down != 8 || span != 0 || peak != 1 {
		t.Fatalf("单样本应回退单次采样：up=%d down=%d span=%d peak=%d", up, down, span, peak)
	}
	// 跨度不足最小窗口（10s）→ 回退单次采样
	up, down, span, _ = nodeWindowSummary([]nodeRateSample{
		{at: 1_000, up: 100, down: 200},
		{at: 5_000, up: 900, down: 1000},
	}, 7, 8)
	if up != 7 || down != 8 || span != 0 {
		t.Fatalf("跨度不足应回退单次采样：up=%d down=%d span=%d", up, down, span)
	}
	// sing-box 重启（计数回退）→ 窗口不可比，回退单次采样
	up, down, span, _ = nodeWindowSummary([]nodeRateSample{
		{at: 0, up: 9000, down: 9000},
		{at: 60_000, up: 100, down: 100},
	}, 7, 8)
	if up != 7 || down != 8 || span != 0 {
		t.Fatalf("重启回退应回退单次采样：up=%d down=%d span=%d", up, down, span)
	}
	// 空窗口 → 单次采样
	up, down, span, peak = nodeWindowSummary(nil, 0, 0)
	if up != 0 || down != 0 || span != 0 || peak != 0 {
		t.Fatalf("空窗口 = %d/%d/%d/%d", up, down, span, peak)
	}
}

func TestNodeRateHistoryWindowAndPrune(t *testing.T) {
	h := newNodeRateHistory()
	now := int64(1_000_000)
	h.record(7, now-400_000, 1, 1, 1) // 窗口外
	h.record(7, now-120_000, 100, 200, 2)
	h.record(7, now-60_000, 300, 400, 4)
	h.record(7, now, 500, 600, 6)
	got := h.window(7, now)
	if len(got) != 3 {
		t.Fatalf("窗口内样本数 = %d，期望 3", len(got))
	}
	if got[0].up != 100 || got[2].up != 500 || got[2].connCount != 6 {
		t.Fatalf("窗口样本 = %+v", got)
	}
	// 记录时的裁剪：远早于当前窗口的样本会被丢掉
	h.record(7, now+300_000, 900, 1000, 9)
	got = h.window(7, now+300_000)
	for _, s := range got {
		if s.at < now+300_000-statsNodeRateWindowMs {
			t.Fatalf("窗口外样本未被裁剪：%+v", s)
		}
	}
	// 未知节点返回空
	if len(h.window(404, now)) != 0 {
		t.Fatal("未知节点不应有样本")
	}
	// 上限保护：样本数不超过 statsNodeRateMaxSamples
	for i := 0; i < statsNodeRateMaxSamples+50; i++ {
		h.record(9, now+int64(i), int64(i), int64(i), 0)
	}
	if n := len(h.window(9, now+statsNodeRateMaxSamples+49)); n > statsNodeRateMaxSamples {
		t.Fatalf("样本数 = %d，超过上限 %d", n, statsNodeRateMaxSamples)
	}
}

// 窗口滚动：超过 5 分钟的样本不再参与平均（不会把陈年速率一直平均下去）。
func TestNodeRateHistoryWindowRolling(t *testing.T) {
	h := newNodeRateHistory()
	base := int64(10_000_000)
	h.record(3, base, 1_000, 2_000, 1)
	h.record(3, base+60_000, 3_000, 4_000, 2)
	if got := h.window(3, base+120_000); len(got) != 2 {
		t.Fatalf("窗口内样本数 = %d，期望 2", len(got))
	}
	// 4 分钟后：两条都还在 5 分钟窗口内
	if got := h.window(3, base+240_000); len(got) != 2 {
		t.Fatalf("4 分钟后窗口内样本数 = %d，期望 2", len(got))
	}
	// 6 分钟后：最早一条滚出窗口，只剩第二秒的样本
	got := h.window(3, base+360_000)
	if len(got) != 1 || got[0].up != 3_000 {
		t.Fatalf("滚动后窗口样本 = %+v，期望只剩 up=3000", got)
	}
}

// sing-box 重启后，重启前的样本不得参与「5 分钟平均速率」（不跨重启拼接）：
// 重启前样本仍在窗口内时回退到单次采样；等它滚出窗口后才用重启后的样本计算。
func TestNodeWindowRestartNotPolluted(t *testing.T) {
	h := newNodeRateHistory()
	base := int64(20_000_000)
	h.record(5, base, 1_000_000, 2_000_000, 1) // 重启前
	h.record(5, base+60_000, 100, 200, 2)      // 重启后
	h.record(5, base+120_000, 6_100, 6_200, 3) // 重启后
	up, down, span, peak := nodeWindowSummary(h.window(5, base+120_000), 7, 8)
	if up != 7 || down != 8 || span != 0 {
		t.Fatalf("含重启前样本时应回退单次采样：up=%d down=%d span=%d", up, down, span)
	}
	if peak != 3 {
		t.Fatalf("连接峰值 = %d，期望 3", peak)
	}
	// 6 分钟后：重启前样本滚出窗口，只用重启后的样本 → (6100-100)/60s = 100 B/s
	samples := h.window(5, base+360_000)
	if len(samples) != 2 {
		t.Fatalf("滚动后重启样本数 = %d，期望 2", len(samples))
	}
	up, down, span, _ = nodeWindowSummary(samples, 7, 8)
	if span != 60_000 || up != 100 || down != 100 {
		t.Fatalf("重启后窗口平均 = up=%d down=%d span=%d，期望 100/100/60000", up, down, span)
	}
}

// 负值防御：Agent 上报异常负数按 0 处理；绕过 record 的负值样本同样不参与平均。
func TestNodeRateHistoryNegativeDefense(t *testing.T) {
	h := newNodeRateHistory()
	h.record(6, 1_000, -5, -10, -1)
	got := h.window(6, 2_000)
	if len(got) != 1 || got[0].up != 0 || got[0].down != 0 || got[0].connCount != 0 {
		t.Fatalf("负数样本应被归零：%+v", got)
	}
	up, down, span, _ := nodeWindowSummary([]nodeRateSample{
		{at: 0, up: -1, down: -1},
		{at: 60_000, up: 100, down: 100},
	}, 7, 8)
	if up != 7 || down != 8 || span != 0 {
		t.Fatalf("负值样本应回退单次采样：up=%d down=%d span=%d", up, down, span)
	}
}

// 窗口读写并发安全（go test -race 下可验证数据竞争）。
func TestNodeRateHistoryConcurrent(t *testing.T) {
	h := newNodeRateHistory()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				h.record(int64(i%4+1), int64(1_000_000+j*100), int64(j), int64(j), 0)
				_ = h.window(int64(i%4+1), int64(1_000_000+j*100))
			}
		}(i)
	}
	wg.Wait()
}

// /api/stats/traffic/nodes 必须返回节点级在线用户数与窗口平均速率，
// 同时保留原始瞬时连接数 / 单次采样速率。
func TestStatsTrafficNodesOnlineCountAndAvgRate(t *testing.T) {
	statsNodeRates.reset()
	t.Cleanup(statsNodeRates.reset)

	fdb, db := newFakeDB(t)
	now := nowMillis()
	// 节点 1：近 5 分钟有 3 个活跃用户；窗口平均速率 100 B/s（6000B / 60s）
	statsNodeRates.record(1, now-61_000, 1_000, 2_000, 1)
	statsNodeRates.record(1, now-1_000, 7_000, 8_000, 5)

	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM singbox_nodes"):
			return fakeRowsSet(
				[]string{"node_id", "node_name", "server_id", "server_name", "stats_enabled",
					"sampled_at", "conn_count", "up_total", "down_total", "up_rate", "down_rate", "stats_error"},
				[]driver.Value{int64(1), "香港1", int64(7), "香港1", int64(1),
					now - 5_000, int64(3), int64(5000), int64(6000), int64(7), int64(8), ""},
			), nil
		case strings.Contains(q, "FROM user_node_recent"):
			return fakeRowsSet([]string{"node_id", "n"},
				[]driver.Value{int64(1), int64(3)},
				[]driver.Value{int64(2), int64(1)},
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
	row := out.Data[0]
	if got := row["online_count"]; got != float64(3) {
		t.Fatalf("online_count = %v，期望 3", got)
	}
	if got := row["active_window_sec"]; got != float64(300) {
		t.Fatalf("active_window_sec = %v，期望 300", got)
	}
	// 原始值保持不动（tooltip / 排障用）
	if got := row["conn_count"]; got != float64(3) {
		t.Fatalf("conn_count = %v，期望原始瞬时值 3", got)
	}
	if got := row["up_rate"]; got != float64(7) {
		t.Fatalf("up_rate = %v，期望原始单次采样 7", got)
	}
	if got := row["down_rate"]; got != float64(8) {
		t.Fatalf("down_rate = %v，期望原始单次采样 8", got)
	}
	// 窗口平均速率：6000B/60s = 100 B/s；连接峰值 5
	if got := row["up_rate_avg"]; got != float64(100) {
		t.Fatalf("up_rate_avg = %v，期望 100", got)
	}
	if got := row["down_rate_avg"]; got != float64(100) {
		t.Fatalf("down_rate_avg = %v，期望 100", got)
	}
	if got := row["rate_window_sec"]; got != float64(60) {
		t.Fatalf("rate_window_sec = %v，期望 60", got)
	}
	if got := row["conn_peak"]; got != float64(5) {
		t.Fatalf("conn_peak = %v，期望 5", got)
	}

	// SQL 必须读取 user_node_recent 且窗口参数 = now-5min
	var found bool
	for _, call := range fdb.queryCalls() {
		if !strings.Contains(call.Query, "FROM user_node_recent") {
			continue
		}
		found = true
		if len(call.Args) != 1 {
			t.Fatalf("在线用户查询参数 = %#v", call.Args)
		}
		arg, ok := call.Args[0].(int64)
		if !ok {
			t.Fatalf("在线用户查询窗口参数类型 = %T", call.Args[0])
		}
		if diff := (now - statsNodeOnlineWindowMs) - arg; diff > 5_000 || diff < -5_000 {
			t.Fatalf("在线用户窗口参数 = %d，期望约 %d", arg, now-statsNodeOnlineWindowMs)
		}
	}
	if !found {
		t.Fatal("统计接口未查询 user_node_recent（在线用户数）")
	}
}

// 节点在线用户数查询失败时降级为 0，不影响接口可用性。
func TestNodeOnlineCountsQueryErrorDegrades(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM user_node_recent") {
			return nil, errFakeUnavailable
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	if got := h.nodeOnlineCounts(t.Context(), nowMillis()); len(got) != 0 {
		t.Fatalf("查询失败应返回空 map，得到 %#v", got)
	}
}

// /api/traffic/nodes 的「活跃 UUID（online）」必须来自 user_node_recent 近 5 分钟，
// 不能再用 user_traffic_hourly.hour_ts（整点桶，除每小时前 5 分钟外恒为 0）。
func TestTrafficForSingboxNodesOnlineFromRecent(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM singbox_nodes"):
			return fakeRowsSet(
				[]string{"id", "name", "server_id", "server_name", "total", "today", "week"},
				[]driver.Value{int64(1), "n1", int64(7), "s1", int64(100), int64(50), int64(80)},
				[]driver.Value{int64(2), "n2", int64(8), "s2", int64(200), int64(60), int64(90)},
			), nil
		case strings.Contains(q, "FROM user_node_recent"):
			return fakeRowsSet([]string{"node_id", "n"},
				[]driver.Value{int64(1), int64(3)},
			), nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	rows, err := h.trafficForSingboxNodes(t.Context(), 0, 0)
	if err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %#v", rows)
	}
	if got := rows[0]["online"]; got != int64(3) {
		t.Fatalf("节点 1 online = %v，期望 3（来自 user_node_recent）", got)
	}
	if got := rows[0]["uuid_count"]; got != int64(3) {
		t.Fatalf("节点 1 uuid_count = %v，期望 3", got)
	}
	if got := rows[1]["online"]; got != int64(0) {
		t.Fatalf("节点 2 online = %v，期望 0", got)
	}
	// 回归：不得再使用整点桶判定在线（hour_ts 作为在线窗口是错的）
	for _, call := range fdb.queryCalls() {
		if strings.Contains(call.Query, "COUNT(DISTINCT CASE WHEN t.hour_ts") {
			t.Fatalf("仍在用 user_traffic_hourly.hour_ts 判定在线：%s", call.Query)
		}
	}
}

// 轮询间隔：当前 Agent（1.2）必须按服务端配置下发 20/60s；
// 极老/未知版本才兜底 10s（老 Agent 没有 WS/长轮询唤醒）。
func TestAgentNextPollSeconds(t *testing.T) {
	cases := []struct {
		name       string
		configured int64
		version    string
		want       int64
	}{
		{"当前 Agent 60s", 60, "1.2", 60},
		{"当前 Agent 20s", 20, "1.2", 20},
		{"上一版重编号 Agent 15s", 15, "1.1", 15},
		{"老 Agent 兜底 10s", 60, "0.9", 10},
		{"版本未知兜底 10s", 60, "", 10},
		{"配置本身 <=10s 不放大", 5, "0.9", 5},
		{"更老的 1.5.x（有长轮询）不兜底", 60, "1.5.4", 60},
		{"配置为 0 原样返回", 0, "1.2", 0},
	}
	for _, tc := range cases {
		if got := agentNextPollSeconds(tc.configured, tc.version); got != tc.want {
			t.Fatalf("%s：agentNextPollSeconds(%d,%q) = %d，期望 %d",
				tc.name, tc.configured, tc.version, got, tc.want)
		}
	}
}
