package runner

import (
	"fmt"
	"os/exec"
	"strings"
)

// SSH 安全防护：开启后只允许组网（Cloudflare WARP）网段访问 SSH。
// 关闭后恢复为允许所有来源。
const (
	sshGuardChain = "PP_SSH_GUARD"
	sshGuardV4    = "100.96.0.0/12"
	sshGuardV6    = "2606:4700:cf1:1000::/64"
)

// runCmd 执行命令并返回合并输出（失败也算，用于日志）。
func runCmd(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// sshPorts 返回 SSH 监听的端口列表，默认 22。
func sshPorts() []string {
	seen := map[string]bool{}
	ports := []string{}
	if out, err := runCmd("/bin/sh", "-c", "ss -tlnp 2>/dev/null | grep -E 'sshd|ssh' | awk '{print $4}'"); err == nil && out != "" {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			idx := strings.LastIndex(line, ":")
			if idx < 0 || idx == len(line)-1 {
				continue
			}
			p := strings.TrimSpace(line[idx+1:])
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			ports = append(ports, p)
		}
	}
	if len(ports) == 0 {
		ports = []string{"22"}
	}
	return ports
}

// chainExists 判断链是否已存在。
func chainExists(bin, chain string) bool {
	_, err := runCmd(bin, "-n", "-L", chain)
	return err == nil
}

// applySSHGuard 开启防护：放行组网网段 + 已建立连接，其余丢弃。
func applySSHGuard() (int, string) {
	ports := sshPorts()
	var logs []string

	for _, item := range []struct {
		bin, cidr, family string
	}{
		{"iptables", sshGuardV4, "IPv4"},
		{"ip6tables", sshGuardV6, "IPv6"},
	} {
		bin := item.bin
		if _, err := exec.LookPath(bin); err != nil {
			logs = append(logs, item.family+" 规则工具不可用，已跳过")
			continue
		}
		if !chainExists(bin, sshGuardChain) {
			if out, err := runCmd(bin, "-N", sshGuardChain); err != nil {
				logs = append(logs, fmt.Sprintf("%s 建链失败：%v %s", item.family, err, out))
				continue
			}
		}
		_, _ = runCmd(bin, "-F", sshGuardChain)
		if out, err := runCmd(bin, "-A", sshGuardChain, "-s", item.cidr, "-j", "ACCEPT"); err != nil {
			logs = append(logs, fmt.Sprintf("%s 放行组网失败：%v %s", item.family, err, out))
		}
		_, _ = runCmd(bin, "-A", sshGuardChain, "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT")
		if out, err := runCmd(bin, "-A", sshGuardChain, "-j", "DROP"); err != nil {
			logs = append(logs, fmt.Sprintf("%s 丢弃规则失败：%v %s", item.family, err, out))
		}
		for _, p := range ports {
			// 已有则跳过，避免重复插入
			if _, err := runCmd(bin, "-C", "INPUT", "-p", "tcp", "--dport", p, "-j", sshGuardChain); err == nil {
				continue
			}
			if out, err := runCmd(bin, "-I", "INPUT", "-p", "tcp", "--dport", p, "-j", sshGuardChain); err != nil {
				logs = append(logs, fmt.Sprintf("%s 端口 %s 挂载失败：%v %s", item.family, p, err, out))
			}
		}
	}

	detail := strings.Join(ports, ",")
	if len(logs) == 0 {
		return 0, "SSH 安全防护已开启：仅允许 " + sshGuardV4 + " 与 " + sshGuardV6 + " 访问端口 " + detail
	}
	return 1, strings.Join(logs, "；")
}

// clearSSHGuard 关闭防护：移除挂载与链，恢复允许所有来源。
func clearSSHGuard() (int, string) {
	ports := sshPorts()
	for _, bin := range []string{"iptables", "ip6tables"} {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		for _, p := range ports {
			for i := 0; i < 8; i++ {
				if _, err := runCmd(bin, "-C", "INPUT", "-p", "tcp", "--dport", p, "-j", sshGuardChain); err != nil {
					break
				}
				_, _ = runCmd(bin, "-D", "INPUT", "-p", "tcp", "--dport", p, "-j", sshGuardChain)
			}
		}
		if chainExists(bin, sshGuardChain) {
			_, _ = runCmd(bin, "-F", sshGuardChain)
			_, _ = runCmd(bin, "-X", sshGuardChain)
		}
	}
	return 0, "SSH 安全防护已关闭：允许所有 IP 访问 SSH"
}

// SSHGuardActive 判断防护当前是否生效（供监控上报使用）。
func SSHGuardActive() bool {
	for _, bin := range []string{"iptables", "ip6tables"} {
		if _, err := exec.LookPath(bin); err != nil {
			continue
		}
		if _, err := runCmd(bin, "-C", "INPUT", "-p", "tcp", "--dport", "22", "-j", sshGuardChain); err == nil {
			return true
		}
	}
	return false
}
