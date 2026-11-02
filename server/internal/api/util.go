package api

import "time"

func nowMillis() int64 { return time.Now().UnixMilli() }

// clampInt 复刻 Worker 的 intOr 语义：只做上下限收敛（0 会落到下限）。
func clampInt(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
