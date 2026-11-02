package api

import (
	"testing"
	"time"
)

func TestResolveLogCutoffDays(t *testing.T) {
	cutoff, errMsg := resolveLogCutoff(float64(30), "")
	if errMsg != "" {
		t.Fatalf("err = %s", errMsg)
	}
	ts, err := time.Parse(time.RFC3339, cutoff)
	if err != nil {
		t.Fatalf("cutoff 不是 RFC3339: %q", cutoff)
	}
	diff := time.Since(ts)
	if diff < 29*24*time.Hour || diff > 31*24*time.Hour {
		t.Fatalf("cutoff 偏差过大: %v", diff)
	}

	if _, msg := resolveLogCutoff(float64(0), ""); msg == "" {
		t.Fatal("days=0 应报错")
	}
	if _, msg := resolveLogCutoff(float64(4000), ""); msg == "" {
		t.Fatal("days 超上限应报错")
	}
}

func TestResolveLogCutoffBefore(t *testing.T) {
	cutoff, errMsg := resolveLogCutoff(nil, "2024-01-02")
	if errMsg != "" || cutoff != "2024-01-02T00:00:00Z" {
		t.Fatalf("cutoff=%q err=%q", cutoff, errMsg)
	}
	if _, msg := resolveLogCutoff(nil, "2024-13-40"); msg == "" {
		t.Fatal("非法日期应报错")
	}
	future := time.Now().UTC().AddDate(0, 0, 2).Format("2006-01-02")
	if _, msg := resolveLogCutoff(nil, future); msg == "" {
		t.Fatal("未来日期应报错")
	}
	if _, msg := resolveLogCutoff(nil, ""); msg == "" {
		t.Fatal("缺失参数应报错")
	}
}

func TestParseTruthy(t *testing.T) {
	for _, v := range []any{true, float64(1), "1", "true"} {
		if !parseTruthy(v) {
			t.Errorf("%v 应为 true", v)
		}
	}
	for _, v := range []any{false, float64(0), "0", "", nil} {
		if parseTruthy(v) {
			t.Errorf("%v 应为 false", v)
		}
	}
}
