// Package installer 安装 / 卸载 Agent：写二进制、配置与 systemd 单元，并启用服务。
//
// root 参数便于单测（生产传 "/"，测试传临时目录，此时不会调用 systemctl）。
package installer

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"proxypilot-agent/internal/config"
)

const (
	// BinPath Agent 二进制路径。
	BinPath = "/usr/local/bin/proxypilot-agent"
	// ConfPath 配置文件路径。
	ConfPath = "/etc/proxypilot-agent.conf"
	// UnitPath systemd 单元路径。
	UnitPath = "/etc/systemd/system/proxypilot-agent.service"
)

// Runner 执行外部命令（单测注入）。
type Runner func(name string, args ...string) (string, error)

// DefaultRunner 真实执行（合并 stderr）。
func DefaultRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// RenderUnit 渲染 systemd 单元（常驻服务，崩溃自动重启）。
func RenderUnit(binPath string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=ProxyPilot Agent (heartbeat / metrics / config deploy)",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + binPath + " run",
		"Restart=always",
		"RestartSec=5",
		"StandardOutput=journal",
		"StandardError=journal",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

// Install 安装：复制二进制 → 写配置(600) → 写单元 → 启用并启动服务。
// srcBin 为待安装的二进制路径（通常是 /proc/self/exe）。
func Install(root string, cfg config.Config, srcBin string, r Runner, log func(string, ...any)) error {
	if r == nil {
		r = DefaultRunner
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	binPath := filepath.Join(root, BinPath)
	confPath := filepath.Join(root, ConfPath)
	unitPath := filepath.Join(root, UnitPath)
	for _, dir := range []string{filepath.Dir(binPath), filepath.Dir(confPath), filepath.Dir(unitPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建目录失败（%s）：%v", dir, err)
		}
	}
	if err := copyFile(srcBin, binPath, 0o700); err != nil {
		return fmt.Errorf("安装二进制失败：%v", err)
	}
	if err := os.WriteFile(confPath, []byte(cfg.String()), 0o600); err != nil {
		return fmt.Errorf("写入配置失败：%v", err)
	}
	if err := os.WriteFile(unitPath, []byte(RenderUnit(BinPath)), 0o644); err != nil {
		return fmt.Errorf("写入 systemd 单元失败：%v", err)
	}
	log("已写入 %s / %s / %s", BinPath, ConfPath, UnitPath)
	if root != "/" {
		return nil // 单测模式：不触碰 systemctl
	}
	if _, err := r("systemctl", "daemon-reload"); err != nil {
		log("systemctl 不可用（%v）：请手动启动 —— %s run", err, BinPath)
		log("crontab 兜底示例：*/1 * * * * %s once", BinPath)
		return nil
	}
	if out, err := r("systemctl", "enable", "--now", "proxypilot-agent.service"); err != nil {
		return fmt.Errorf("启用服务失败：%v（%s）", err, strings.TrimSpace(out))
	}
	log("服务已启动：systemctl status proxypilot-agent")
	return nil
}

// Uninstall 卸载：停服务 → 删单元/二进制/配置 → daemon-reload。
func Uninstall(root string, r Runner, log func(string, ...any)) error {
	if r == nil {
		r = DefaultRunner
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	if root == "/" {
		_, _ = r("systemctl", "disable", "--now", "proxypilot-agent.service")
		_, _ = r("systemctl", "daemon-reload")
	}
	for _, path := range []string{filepath.Join(root, UnitPath), filepath.Join(root, BinPath), filepath.Join(root, ConfPath)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除失败（%s）：%v", path, err)
		}
	}
	log("已卸载（状态目录保留，可手动删除 /var/lib/proxypilot-agent）")
	return nil
}

// copyFile 复制文件并设置权限（源与目标相同则跳过）。
func copyFile(src, dst string, mode os.FileMode) error {
	if src == "" {
		return fmt.Errorf("未指定二进制来源")
	}
	if absSrc, err := filepath.Abs(src); err == nil {
		if absDst, err2 := filepath.Abs(dst); err2 == nil && absSrc == absDst {
			return nil
		}
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}
