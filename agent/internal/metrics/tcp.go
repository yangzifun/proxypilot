package metrics

import "strings"

// tcpPorts 汇总本机监听端口（等价于 shell agent 的 `ss -H -tln | awk '{print $4}'`）。
// 读取 /proc/net/tcp 与 /proc/net/tcp6；上限 300 字节（与服务端约定的截断一致）。
func tcpPorts() string {
	list := append(ParseProcNetTCP(readFile("/proc/net/tcp"), false), ParseProcNetTCP(readFile("/proc/net/tcp6"), true)...)
	seen := map[string]bool{}
	uniq := make([]string, 0, len(list))
	for _, item := range list {
		if seen[item] {
			continue
		}
		seen[item] = true
		uniq = append(uniq, item)
	}
	out := strings.Join(uniq, ",")
	if len(out) > 300 {
		out = out[:300]
	}
	return out
}
