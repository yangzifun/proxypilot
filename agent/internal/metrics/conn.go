package metrics

import "strings"

// ConnStates TCP/UDP 连接状态计数（/proc/net/tcp(6) + /proc/net/udp(6) 汇总）。
type ConnStates struct {
	Total       int `json:"total"`
	Established int `json:"established"`
	Listen      int `json:"listen"`
	TimeWait    int `json:"time_wait"`
	CloseWait   int `json:"close_wait"`
	SynSent     int `json:"syn_sent"`
	Other       int `json:"other"`
}

// ParseProcNetStates 统计 /proc/net/{tcp,tcp6,udp,udp6} 中每行的 st 字段（第 4 列，十六进制）：
//
//	01 ESTABLISHED · 02 SYN_SENT · 03 SYN_RECV · 04 FIN_WAIT1 · 05 FIN_WAIT2
//	06 TIME_WAIT · 07 CLOSE · 08 CLOSE_WAIT · 09 LAST_ACK · 0A LISTEN · 0B CLOSING
func ParseProcNetStates(text string) ConnStates {
	out := ConnStates{}
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if i == 0 { // 表头
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		state := strings.ToUpper(fields[3])
		if len(state) != 2 {
			continue
		}
		out.Total++
		switch state {
		case "01":
			out.Established++
		case "02":
			out.SynSent++
		case "06":
			out.TimeWait++
		case "08":
			out.CloseWait++
		case "0A":
			out.Listen++
		default:
			out.Other++
		}
	}
	return out
}

// sumConn 合并多个来源（tcp + tcp6 + udp + udp6）。
func sumConn(list ...ConnStates) ConnStates {
	out := ConnStates{}
	for _, c := range list {
		out.Total += c.Total
		out.Established += c.Established
		out.Listen += c.Listen
		out.TimeWait += c.TimeWait
		out.CloseWait += c.CloseWait
		out.SynSent += c.SynSent
		out.Other += c.Other
	}
	return out
}

// connStates 读取并汇总本机 TCP/UDP 连接状态（读不到则全 0）。
func connStates() ConnStates {
	return sumConn(
		ParseProcNetStates(readFile("/proc/net/tcp")),
		ParseProcNetStates(readFile("/proc/net/tcp6")),
		ParseProcNetStates(readFile("/proc/net/udp")),
		ParseProcNetStates(readFile("/proc/net/udp6")),
	)
}
