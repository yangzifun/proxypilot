package metrics

import (
	"strconv"
	"strings"
)

// CPUStat 表示 /proc/stat 的 cpu 汇总行拆解：
// busy = user+nice+system+irq+softirq，idle = idle+iowait，steal = 被宿主机抢占的时间。
type CPUStat struct {
	Busy  int64
	Idle  int64
	Steal int64
}

// ParseCPUStatFull 解析 /proc/stat 的 cpu 汇总行（完整拆解）。
// 云主机常见 steal 很高：此时总时间几乎都不是「idle」，但如果把 steal 当成自己的
// 占用，就会得到长期 100% 的假告警。必须把 steal 单列出来。
func ParseCPUStatFull(text string) CPUStat {
	var st CPUStat
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		at := func(i int) int64 {
			if i >= len(fields) {
				return 0
			}
			n, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				return 0
			}
			return n
		}
		// fields: user nice system idle iowait irq softirq steal guest guest_nice
		st.Busy = at(0) + at(1) + at(2) + at(5) + at(6)
		st.Idle = at(3) + at(4)
		st.Steal = at(7)
		return st
	}
	return st
}