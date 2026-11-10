package main

import (
	"testing"
	"time"
)

func TestPct(t *testing.T) {
	cases := []struct {
		part, total uint64
		want        float64
	}{
		{0, 0, 0},
		{0, 10, 0},
		{3, 10, 30},
		{1, 3, 100.0 / 3},
		{10, 10, 100},
	}
	for _, c := range cases {
		got := pct(c.part, c.total)
		if diff := got - c.want; diff > 1e-9 || diff < -1e-9 {
			t.Fatalf("pct(%d,%d)=%v，期望 %v", c.part, c.total, got, c.want)
		}
	}
}

func TestFormatQualityInfo(t *testing.T) {
	got := formatQualityInfo(143 * time.Millisecond)
	if got != "隧道往返 143ms" {
		t.Fatalf("格式化结果不符：%q", got)
	}
	// 143.5ms 四舍五入到 144ms
	got = formatQualityInfo(143500 * time.Microsecond)
	if got != "隧道往返 144ms" {
		t.Fatalf("四舍五入结果不符：%q", got)
	}
}

// 质量采样的阈值语义：连续失败 >= qualityFailThreshold 才判定不通。
// 这里只断言常量关系，避免以后有人把阈值改成 1 导致单次抖动就翻脸。
func TestQualityThresholdSane(t *testing.T) {
	if qualityFailThreshold < 2 {
		t.Fatalf("连续失败阈值 %d 太小，单次抖动就会误判为不通", qualityFailThreshold)
	}
	if qualityInterval <= 0 || qualityTimeout <= 0 {
		t.Fatal("采样间隔与超时都必须为正")
	}
	// 超时必须小于间隔，否则会堆积
	if qualityTimeout >= qualityInterval {
		t.Fatalf("超时 %v 不应大于等于采样间隔 %v", qualityTimeout, qualityInterval)
	}
}