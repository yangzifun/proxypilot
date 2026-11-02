//go:build !unix

package metrics

// DiskPercent 非 Unix 平台无 statfs，返回 0（Agent 的生产目标平台是 Linux）。
func DiskPercent(path string) float64 { return 0 }
