package api

import (
	"database/sql/driver"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientErrorsListFilters(t *testing.T) {
	fdb, db := newFakeDB(t)
	var selectQuery string
	var selectArgs []any
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "SELECT COUNT(*) FROM client_errors") {
			return fakeRowsSet([]string{"n"}, []driver.Value{int64(0)}), nil
		}
		if strings.Contains(q, "FROM client_errors") {
			selectQuery = q
			selectArgs = fakeArgsAny(args)
			return &fakeRows{}, nil
		}
		return &fakeRows{}, nil
	}
	h := &handlers{db: db, log: testLogger()}
	req := httptest.NewRequest(http.MethodGet,
		"/api/client-errors?q=needle&from=1000&to=5000&mode=admin&page=2&size=10", nil)
	rec := httptest.NewRecorder()
	h.clientErrorsList(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list status=%d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(selectQuery, "LOCATE(?, message)") ||
		!strings.Contains(selectQuery, "created_at >= ?") ||
		!strings.Contains(selectQuery, "created_at <= ?") {
		t.Fatalf("过滤 SQL 缺失：%s", selectQuery)
	}
	// mode, q×3, from, to, size, offset
	if len(selectArgs) != 8 || selectArgs[0] != "admin" ||
		selectArgs[1] != "needle" || selectArgs[2] != "needle" || selectArgs[3] != "needle" ||
		selectArgs[4] != int64(1000) || selectArgs[5] != int64(5000) ||
		selectArgs[6] != int64(10) || selectArgs[7] != int64(10) {
		t.Fatalf("过滤参数异常：%#v", selectArgs)
	}

	// 非法时间/逆序时间区间。
	for _, path := range []string{
		"/api/client-errors?from=abc",
		"/api/client-errors?to=0",
		"/api/client-errors?from=5000&to=1000",
	} {
		rec = httptest.NewRecorder()
		h.clientErrorsList(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s 应 400，实际 %d", path, rec.Code)
		}
	}
}

func clientErrorStatsFakeDB(t *testing.T, empty bool) (*fakeDB, *handlers) {
	t.Helper()
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "COUNT(*), COALESCE(MAX(created_at)"):
			if empty {
				return fakeRowsSet([]string{"total", "latest"}, []driver.Value{int64(0), int64(0)}), nil
			}
			return fakeRowsSet([]string{"total", "latest"}, []driver.Value{int64(5), int64(1800000000000)}), nil
		case strings.Contains(q, "GROUP BY mode"):
			if empty {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"mode", "cnt"},
				[]driver.Value{"admin", int64(3)},
				[]driver.Value{"portal", int64(2)},
			), nil
		case strings.Contains(q, "AS bucket"):
			if empty {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"bucket", "cnt"},
				[]driver.Value{int64(1728000000000), int64(2)},
				[]driver.Value{int64(1728086400000), int64(3)},
			), nil
		case strings.Contains(q, "GROUP BY message"):
			if empty {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"message", "cnt"},
				[]driver.Value{strings.Repeat("m", 300), int64(4)},
			), nil
		case strings.Contains(q, "GROUP BY page"):
			if empty {
				return &fakeRows{}, nil
			}
			return fakeRowsSet([]string{"page", "cnt"},
				[]driver.Value{"/page", int64(5)},
			), nil
		}
		return &fakeRows{}, nil
	}
	return fdb, &handlers{db: db, log: testLogger()}
}

func TestClientErrorStatsRanges(t *testing.T) {
	for _, tc := range []struct {
		rangeKey    string
		granularity string
		bucketArg   int64
	}{
		{rangeKey: "24h", granularity: "hour", bucketArg: 3600 * 1000},
		{rangeKey: "7d", granularity: "day", bucketArg: 24 * 3600 * 1000},
		{rangeKey: "30d", granularity: "day", bucketArg: 24 * 3600 * 1000},
	} {
		t.Run(tc.rangeKey, func(t *testing.T) {
			fdb, h := clientErrorStatsFakeDB(t, false)
			rec := httptest.NewRecorder()
			h.clientErrorStats(rec, httptest.NewRequest(http.MethodGet, "/api/client-errors/stats?range="+tc.rangeKey, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("stats status=%d body=%s", rec.Code, rec.Body.String())
			}
			var out struct {
				Range       string           `json:"range"`
				Granularity string           `json:"granularity"`
				Total       int64            `json:"total"`
				ByMode      []map[string]any `json:"by_mode"`
				Trend       []map[string]any `json:"trend"`
				TopMessages []map[string]any `json:"top_messages"`
				TopPages    []map[string]any `json:"top_pages"`
				LatestAt    int64            `json:"latest_at"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Range != tc.rangeKey || out.Granularity != tc.granularity || out.Total != 5 || out.LatestAt != 1800000000000 {
				t.Fatalf("stats header 异常：%+v", out)
			}
			if len(out.ByMode) != 2 || len(out.Trend) != 2 || len(out.TopMessages) != 1 || len(out.TopPages) != 1 {
				t.Fatalf("stats 数据异常：%+v", out)
			}
			if len([]rune(out.TopMessages[0]["message"].(string))) != 160 {
				t.Fatalf("top message 未按 160 截断：%d", len([]rune(out.TopMessages[0]["message"].(string))))
			}
			var bucketSeen bool
			for _, call := range fdb.queryCalls() {
				if strings.Contains(call.Query, "AS bucket") {
					bucketSeen = true
					if len(call.Args) < 1 || call.Args[0] != tc.bucketArg {
						t.Fatalf("趋势 bucket 参数=%#v，期望 %d", call.Args, tc.bucketArg)
					}
				}
			}
			if !bucketSeen {
				t.Fatal("未执行趋势查询")
			}
		})
	}

	// 默认 range=7d；非法 range 400。
	fdb, h := clientErrorStatsFakeDB(t, false)
	rec := httptest.NewRecorder()
	h.clientErrorStats(rec, httptest.NewRequest(http.MethodGet, "/api/client-errors/stats", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"range":"7d"`) {
		t.Fatalf("默认 range 异常：%d %s", rec.Code, rec.Body.String())
	}
	_ = fdb
	rec = httptest.NewRecorder()
	h.clientErrorStats(rec, httptest.NewRequest(http.MethodGet, "/api/client-errors/stats?range=1h", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法 range 应 400：%d", rec.Code)
	}
}

func TestClientErrorStatsEmpty(t *testing.T) {
	_, h := clientErrorStatsFakeDB(t, true)
	rec := httptest.NewRecorder()
	h.clientErrorStats(rec, httptest.NewRequest(http.MethodGet, "/api/client-errors/stats?range=24h", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("空数据 stats status=%d body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"by_mode", "trend", "top_messages", "top_pages"} {
		arr, ok := out[key].([]any)
		if !ok || len(arr) != 0 {
			t.Fatalf("%s 空数据应返回空数组：%#v", key, out[key])
		}
	}
	if out["total"] != float64(0) || out["latest_at"] != float64(0) {
		t.Fatalf("空数据 total/latest 异常：%#v", out)
	}
}
