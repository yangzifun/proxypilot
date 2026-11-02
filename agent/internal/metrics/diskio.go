package metrics

import (
	"strings"
)

// DiskIO 磁盘累计读写（/proc/diskstats 汇总，扇区按 512 字节换算）。
type DiskIO struct {
	ReadBytes  int64 `json:"read_bytes"`
	WriteBytes int64 `json:"write_bytes"`
	Reads      int64 `json:"reads"`
	Writes     int64 `json:"writes"`
	Devices    int   `json:"devices"`
}

// skipDisk 是否跳过该块设备：
//   - loop*/ram*/zram*：回环与内存盘（非真实磁盘）
//   - sr*：光驱
//   - dm-*：device-mapper（LVM/加密卷），与底层物理盘重复计数
//   - md*：软 RAID，与成员盘重复计数
//   - 带分区后缀（sda1 / nvme0n1p2 / vda1）：与整盘重复计数
func skipDisk(name string) bool {
	if name == "" {
		return true
	}
	for _, prefix := range []string{"loop", "ram", "zram", "sr", "dm-", "md", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	// 分区：以数字结尾（sda1 / vda12 / nvme0n1p3 ...）——整盘名不会以数字结尾（除 nvme0n1，它属于整盘）
	if isPartitionName(name) {
		return true
	}
	return false
}

// isPartitionName 判断是否为分区名（在整盘名后追加了数字后缀）。
// 规则：出现 "p<数字>" 结尾（nvme/mmc 风格）或以数字结尾且去掉尾部数字后仍是合法整盘名（sd/vd/xvd/hd 风格）。
func isPartitionName(name string) bool {
	if name == "" {
		return false
	}
	last := name[len(name)-1]
	if last < '0' || last > '9' {
		return false
	}
	// nvme0n1 → 整盘；nvme0n1p1 → 分区
	if i := strings.LastIndex(name, "p"); i > 0 && name[i-1] >= '0' && name[i-1] <= '9' {
		return true
	}
	base := strings.TrimRight(name, "0123456789")
	switch {
	case strings.HasPrefix(base, "sd"), strings.HasPrefix(base, "vd"), strings.HasPrefix(base, "xvd"), strings.HasPrefix(base, "hd"):
		return true
	}
	return false
}

// ParseDiskStats 解析 /proc/diskstats：
//
//	1 major  2 minor  3 name  4 reads  5 reads_merged  6 sectors_read  7 ms_reading
//	8 writes 9 writes_merged 10 sectors_written 11 ms_writing ...
func ParseDiskStats(text string) DiskIO {
	out := DiskIO{}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		name := fields[2]
		if skipDisk(name) {
			continue
		}
		out.Reads += parseCounter(fields[3])
		out.ReadBytes += parseCounter(fields[5]) * 512 // sectors → bytes
		out.Writes += parseCounter(fields[7])
		out.WriteBytes += parseCounter(fields[9]) * 512
		out.Devices++
	}
	return out
}
