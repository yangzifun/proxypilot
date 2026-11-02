package metrics

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// defaultRunner 执行命令并返回 stdout（stderr 合并进 stdout，便于把错误原因带回后台）。
func defaultRunner(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// ParseLoadAvg 解析 /proc/loadavg → "1,5,15"（缺失项补 0，与服务端解析口径一致）。
func ParseLoadAvg(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	parts := make([]string, 3)
	for i := 0; i < 3; i++ {
		if i < len(fields) && isNumber(fields[i]) {
			parts[i] = fields[i]
		} else {
			parts[i] = "0"
		}
	}
	return strings.Join(parts, ",")
}

// ParseUptime 解析 /proc/uptime 首字段（秒，取整）。
func ParseUptime(text string) int64 {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return 0
	}
	f, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f)
}

// ParseMeminfo 解析 /proc/meminfo → (usedMB, totalMB)，单位 MB（向下取整）。
// 口径：优先 MemTotal-MemAvailable（内核估算的真实可用量）；缺失时回落经典公式
// MemTotal-MemFree-Buffers-Cached-SReclaimable+Shmem（与 free 的 used 列近似）。
func ParseMeminfo(text string) (int64, int64) {
	values := map[string]int64{}
	for _, line := range strings.Split(text, "\n") {
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colon])
		fields := strings.Fields(strings.TrimSpace(line[colon+1:]))
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		values[key] = n
	}
	totalKB := values["MemTotal"]
	if totalKB <= 0 {
		return 0, 0
	}
	var usedKB int64
	if avail, ok := values["MemAvailable"]; ok {
		usedKB = totalKB - avail
	} else {
		usedKB = totalKB - values["MemFree"] - values["Buffers"] - values["Cached"] - values["SReclaimable"] + values["Shmem"]
	}
	if usedKB < 0 {
		usedKB = 0
	}
	if usedKB > totalKB {
		usedKB = totalKB
	}
	return usedKB / 1024, totalKB / 1024
}

// ParseCPUStat 解析 /proc/stat 的 cpu 汇总行，只累计「忙时」：
// user(0)+nice(1)+system(2)+irq(5)+softirq(6)+steal(7)，排除 idle(3) 与 iowait(4)。
// 之前把 idle 也算进去，导致 Worker 用两次差值算出的 CPU% 恒接近 100%。
func ParseCPUStat(text string) int64 {
	busyIdx := []int{0, 1, 2, 5, 6, 7}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		var sum int64
		for _, i := range busyIdx {
			if i >= len(fields) {
				continue
			}
			n, err := strconv.ParseInt(fields[i], 10, 64)
			if err != nil {
				n = 0
			}
			sum += n
		}
		return sum
	}
	return 0
}

// ParseOSRelease 解析 /etc/os-release 的 PRETTY_NAME（去引号），与 shell agent 口径一致。
func ParseOSRelease(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PRETTY_NAME=") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "PRETTY_NAME="))
		v = strings.Trim(v, "\"'")
		return v
	}
	return ""
}

// ParseProcNetTCP 解析 /proc/net/tcp(6) 的监听地址（st == 0A 即 LISTEN）→ ["0.0.0.0:22", "[::]:443"]。
func ParseProcNetTCP(text string, ipv6 bool) []string {
	out := []string{}
	for i, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if i == 0 || len(fields) < 4 { // 首行是表头
			continue
		}
		if strings.ToUpper(fields[3]) != "0A" { // 只看 LISTEN
			continue
		}
		addr := ParseProcNetAddr(fields[1], ipv6)
		if addr == "" {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// ParseProcNetAddr 解析 /proc/net/tcp(6) 的 local_address 字段（HEXIP:HEXPORT）。
func ParseProcNetAddr(field string, ipv6 bool) string {
	colon := strings.LastIndexByte(field, ':')
	if colon <= 0 {
		return ""
	}
	port, err := strconv.ParseUint(field[colon+1:], 16, 16)
	if err != nil {
		return ""
	}
	hexIP := field[:colon]
	ip := decodeHexIP(hexIP, ipv6)
	if ip == nil {
		return ""
	}
	if ipv6 {
		return "[" + ip.String() + "]:" + strconv.FormatUint(port, 10)
	}
	return ip.String() + ":" + strconv.FormatUint(port, 10)
}

// decodeHexIP 把 /proc/net 的小端十六进制地址解成 net.IP。
func decodeHexIP(hexIP string, ipv6 bool) net.IP {
	size := 4
	if ipv6 {
		size = 16
	}
	if len(hexIP) != size*2 {
		return nil
	}
	raw := make([]byte, size)
	for i := 0; i < size*2; i += 2 {
		b, err := strconv.ParseUint(hexIP[i:i+2], 16, 8)
		if err != nil {
			return nil
		}
		raw[i/2] = byte(b)
	}
	// IPv4：4 字节整体小端；IPv6：4 个 32 位字各自小端
	if !ipv6 {
		for i, j := 0, 3; i < j; i, j = i+1, j-1 {
			raw[i], raw[j] = raw[j], raw[i]
		}
		return net.IP(raw)
	}
	out := make([]byte, 16)
	for w := 0; w < 4; w++ {
		for i := 0; i < 4; i++ {
			out[w*4+i] = raw[w*4+3-i]
		}
	}
	return net.IP(out)
}

func isNumber(v string) bool {
	if v == "" {
		return false
	}
	_, err := strconv.ParseFloat(v, 64)
	return err == nil
}

// FormatErr 统一错误文案（给日志用）。
func FormatErr(prefix string, err error) string {
	if err == nil {
		return prefix
	}
	return fmt.Sprintf("%s：%v", prefix, err)
}
