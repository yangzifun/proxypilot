//go:build windows

package main

import (
	"log"
	"strings"
)

// disableTunOffload 关闭虚拟网卡上的各类「发送/接收卸载」。
//
// 为什么必须关：Wintun 是纯软件网卡，但 Windows 的 TCP/IP 栈仍可能按
// 网卡上报的能力启用 LSO（Large Send Offload）/ 校验和卸载 / RSC。
// 一旦启用，栈交给「网卡」的报文是未切分的大段——IP 头里的 total length
// 是整段长度，而实际拷贝给驱动的字节数可能只有头部。
//
// 实测症状（节点侧抓到的坏包）：
//
//	frame_len=40  IP total=52   （TCP SYN，TCP 选项整段缺失）
//	frame_len=52  IP total=61
//	frame_len=52  IP total=257
//
// 客户端自检 inner_bad 也会同步计数，且占比稳定在 25% 左右 ——
// 正好等于用户 ping 的丢包率。
//
// 这些都是「尽力而为」的操作：失败只记日志，不影响连接建立。
func disableTunOffload(alias string) {
	if strings.TrimSpace(alias) == "" {
		return
	}
	// 逐项关闭，任何一项不存在都忽略（不同 Windows 版本支持的特性不同）
	steps := []struct {
		name string
		ps   string
	}{
		{"LSO", `Disable-NetAdapterLso -Name $env:PP_TUN -IPv4 -ErrorAction SilentlyContinue`},
		{"ChecksumOffload", `Disable-NetAdapterChecksumOffload -Name $env:PP_TUN -ErrorAction SilentlyContinue`},
		{"RSC", `Disable-NetAdapterRsc -Name $env:PP_TUN -ErrorAction SilentlyContinue`},
		{"RSS", `Disable-NetAdapterRss -Name $env:PP_TUN -ErrorAction SilentlyContinue`},
	}
	for _, s := range steps {
		if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
			"$env:PP_TUN='"+strings.ReplaceAll(alias, "'", "''")+"'; "+s.ps); err != nil {
			log.Printf("关闭网卡 %s 的 %s 失败（可忽略）：%v %s", alias, s.name, err, lastLine(out))
			continue
		}
	}
	log.Printf("已尝试关闭虚拟网卡 %s 的 LSO/校验和/RSC/RSS", alias)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(lines[len(lines)-1])
}