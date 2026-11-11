package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// 节点维护：日志封顶 + 每日定时重启
//
// 背景：sing-box 常驻运行，日志经 journald 累积。默认 journald 上限是磁盘的 10%，
// 机器跑久了 journal 会吃掉几十 GB，触发磁盘写满/变慢。
// 同时长时间运行会累积内存与连接状态，需要每日重启一次。
//
// 设计取舍：
//  1. 日志封顶用 journald drop-in（SystemMaxUse=200M），并在生效后立刻 vacuum
//     一次，把已经占用的空间当场释放；
//  2. 每日重启用 systemd timer + RandomizedDelaySec：所有节点在同一时间窗
//     各自随机延后，天然做到「挨个重启」而不是同时掉线；
//  3. 全部幂等：内容没变就不重启 journald、不重写 unit，避免每次 agent
//     启动都触发 systemd 操作。

const (
	journaldDropIn = "/etc/systemd/journald.conf.d/99-proxypilot.conf"
	rebootService  = "/etc/systemd/system/proxypilot-daily-reboot.service"
	rebootTimer    = "/etc/systemd/system/proxypilot-daily-reboot.timer"
)

// 默认值：每天 04:00 起，随机延后最多 45 分钟（多节点自然错开）
const (
	defaultRebootTime  = "04:00"
	defaultRebootDelay = 2700
	journalMaxUse      = "200M"
	journalMaxFileSize = "50M"
	journalRetention   = "14d"
)

// ensureMaintenance 在每次 agent 启动时对齐节点维护配置（幂等）。
func ensureMaintenance(logf func(string, ...any), rebootEnabled bool, rebootTime string, delaySec int) {
	if os.Geteuid() != 0 {
		return // 非 root 不尝试改系统配置
	}
	if rebootTime == "" {
		rebootTime = defaultRebootTime
	}
	if delaySec <= 0 {
		delaySec = defaultRebootDelay
	}

	ensureJournaldCap(logf)
	ensureRebootTimer(logf, rebootEnabled, rebootTime, delaySec)
}

// ensureJournaldCap 限制 journal 占用，并在首次生效时立即收缩。
func ensureJournaldCap(logf func(string, ...any)) {
	body := strings.Join([]string{
		"# 由 ProxyPilot Agent 写入：限制 journal 磁盘占用，避免长期运行吃满磁盘",
		"[Journal]",
		"SystemMaxUse=" + journalMaxUse,
		"SystemMaxFileSize=" + journalMaxFileSize,
		"MaxRetentionSec=" + journalRetention,
		"",
	}, "\n")
	changed, err := writeIfChanged(journaldDropIn, body)
	if err != nil {
		logf("[maintenance] 写入 journald 限制失败：%v", err)
		return
	}
	if changed {
		logf("[maintenance] 已写入 journald 限制（%s），重启 journald 并收缩日志", journalMaxUse)
		_ = exec.Command("systemctl", "restart", "systemd-journald").Run()
	}
	// 无论是否改动都收缩一次：把历史超限部分立刻释放
	if out, err := exec.Command("journalctl", "--vacuum-size="+journalMaxUse).CombinedOutput(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" && changed {
			logf("[maintenance] journal 收缩：%s", s)
		}
	} else if changed {
		logf("[maintenance] journal 收缩失败：%v", err)
	}
}

// ensureRebootTimer 维护「每日定时重启」的 timer。
//
// 同一时间窗 + 随机延后 = 多台机器挨个重启，不会同时掉线。
func ensureRebootTimer(logf func(string, ...any), enabled bool, hhmm string, delaySec int) {
	svc := strings.Join([]string{
		"# 由 ProxyPilot Agent 写入：每日定时重启（错峰由 OnCalendar + RandomizedDelaySec 保证）",
		"[Unit]",
		"Description=ProxyPilot daily reboot",
		"",
		"[Service]",
		"Type=oneshot",
		"ExecStart=/sbin/shutdown -r +1 \"ProxyPilot 每日维护重启\"",
		"",
	}, "\n")
	timer := strings.Join([]string{
		"[Unit]",
		"Description=ProxyPilot daily reboot timer",
		"",
		"[Timer]",
		"OnCalendar=*-*-* " + hhmm + ":00",
		fmt.Sprintf("RandomizedDelaySec=%d", delaySec),
		"Persistent=true",
		"",
		"[Install]",
		"WantedBy=timers.target",
		"",
	}, "\n")

	c1, e1 := writeIfChanged(rebootService, svc)
	c2, e2 := writeIfChanged(rebootTimer, timer)
	if e1 != nil || e2 != nil {
		logf("[maintenance] 写入重启 timer 失败：%v / %v", e1, e2)
		return
	}
	if c1 || c2 {
		_ = exec.Command("systemctl", "daemon-reload").Run()
		logf("[maintenance] 已更新每日重启 timer（%s，随机延后 %ds）", hhmm, delaySec)
	}

	// 开关状态由配置决定
	if enabled {
		if err := exec.Command("systemctl", "enable", "--now", "proxypilot-daily-reboot.timer").Run(); err != nil {
			logf("[maintenance] 启用每日重启失败：%v", err)
		} else {
			logf("[maintenance] 每日重启已启用：%s + 随机延后 %ds（多机错峰）", hhmm, delaySec)
		}
		return
	}
	_ = exec.Command("systemctl", "disable", "--now", "proxypilot-daily-reboot.timer").Run()
}

// writeIfChanged 内容不同才写入，返回是否发生改动。
func writeIfChanged(path, body string) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && string(old) == body {
		return false, nil
	}
	if err := os.MkdirAll(dirOf(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}