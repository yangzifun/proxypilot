//go:build unix

package metrics

import "syscall"

// DiskPercent 返回挂载点已用百分比（等价于 shell agent 的 `df -P / | awk '{gsub(/%/,"");print $5}'`）。
// 计算口径与 df 一致：used / (used + available)，四舍五入到 1 位小数。
func DiskPercent(path string) float64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	used := float64(st.Blocks - st.Bfree)
	avail := float64(st.Bavail)
	total := used + avail
	if total <= 0 {
		return 0
	}
	percent := used / total * 100
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	// 保留 1 位小数（df 取整到整数，这里更细一点便于看趋势）
	return float64(int64(percent*10+0.5)) / 10
}
