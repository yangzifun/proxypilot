package metrics

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

// TopProcLimit Top 进程采样数量（按 CPU 时间片降序）。
const TopProcLimit = 5

// ProcSample 单个进程采样。
type ProcSample struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Ticks int64  `json:"ticks"`  // utime + stime（jiffies；服务端用两次差值算 CPU%）
	RSSMB int64  `json:"rss_mb"` // 常驻内存（MB）
}

// ProcInfo 进程汇总（进程总数 + Top N）。
type ProcInfo struct {
	Total int          `json:"total"`
	Top   []ProcSample `json:"top"`
}

// ParseProcStat 解析 /proc/<pid>/stat：
//
//	pid (comm) state ppid ... utime(14) stime(15) ... rss(24, 单位=页) ...
//
// comm 可能含空格与括号 → 以最后一个 ')' 为界切分（标准做法）。
func ParseProcStat(text string) (name string, ticks int64, rssPages int64, ok bool) {
	open := strings.IndexByte(text, '(')
	closeIdx := strings.LastIndexByte(text, ')')
	if open < 0 || closeIdx <= open {
		return "", 0, 0, false
	}
	name = text[open+1 : closeIdx]
	rest := strings.Fields(strings.TrimSpace(text[closeIdx+1:]))
	// rest[0] = state（第 3 字段）→ utime 是第 14 字段、stime 第 15、rss 第 24
	// 换算到 rest 中的下标：字段 N → rest[N-3]
	const utimeIdx, stimeIdx, rssIdx = 14 - 3, 15 - 3, 24 - 3
	if len(rest) <= rssIdx {
		return name, 0, 0, false
	}
	utime := parseCounter(rest[utimeIdx])
	stime := parseCounter(rest[stimeIdx])
	return name, utime + stime, parseCounter(rest[rssIdx]), true
}

// ParseProcStatusRSS 解析 /proc/<pid>/status 的 VmRSS（kB → MB），兜底用。
func ParseProcStatusRSS(text string) int64 {
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "VmRSS:")))
		if len(fields) == 0 {
			return 0
		}
		return parseCounter(fields[0]) / 1024
	}
	return 0
}

// ListProcStats 遍历 /proc 目录统计进程（纯 /proc，不依赖 ps；读不到则返回总数 0）。
func ListProcStats() ProcInfo {
	out := ProcInfo{Top: []ProcSample{}}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	pageSize := int64(os.Getpagesize())
	if pageSize <= 0 {
		pageSize = 4096
	}
	samples := make([]ProcSample, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		name, ticks, rssPages, ok := ParseProcStat(readFile("/proc/" + entry.Name() + "/stat"))
		if !ok {
			continue
		}
		out.Total++
		samples = append(samples, ProcSample{
			PID:   pid,
			Name:  truncateProcName(name),
			Ticks: ticks,
			RSSMB: rssPages * pageSize / (1024 * 1024),
		})
	}
	sort.SliceStable(samples, func(i, j int) bool {
		if samples[i].Ticks == samples[j].Ticks {
			return samples[i].RSSMB > samples[j].RSSMB
		}
		return samples[i].Ticks > samples[j].Ticks
	})
	if len(samples) > TopProcLimit {
		samples = samples[:TopProcLimit]
	}
	if samples != nil {
		out.Top = samples
	}
	return out
}

// truncateProcName 进程名最长 64 字节（与 Worker 端截断口径一致）。
func truncateProcName(name string) string {
	if len(name) > 64 {
		return name[:64]
	}
	return name
}
