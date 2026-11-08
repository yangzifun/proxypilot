//go:build linux

package node

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/songgao/water"
)

const (
	nftTable    = "ppvpn"
	nftNATTable = "ppvpn_nat"
)

func openTUN(name string, mtu int) (tunDevice, error) {
	dev, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{Name: name},
	})
	if err != nil {
		return nil, err
	}
	return dev, nil
}

// configureTUN 配置地址/MTU、开启转发，并写入 NAT 与隔离规则。
// 优先用 iptables；系统只有 nft（如 Debian 13 最小镜像）时自动切 nftables。
func configureTUN(cfg Config) error {
	_ = run("ip", "addr", "add", cfg.Gateway4+"/24", "dev", cfg.TunName)
	if err := run("ip", "link", "set", "dev", cfg.TunName, "mtu", strconv.Itoa(cfg.MTU), "up"); err != nil {
		return err
	}
	if err := run("sysctl", "-qw", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}

	wan := cfg.WAN
	if wan == "" {
		wan = defaultWAN()
	}

	if hasBinary("iptables") {
		return configureIPTables(cfg, wan)
	}
	if hasBinary("nft") {
		return configureNFT(cfg, wan)
	}
	return fmt.Errorf("系统既没有 iptables 也没有 nft，无法配置 NAT/隔离")
}

func cleanupTUN(cfg Config) {
	wan := cfg.WAN
	if wan == "" {
		wan = defaultWAN()
	}
	if hasBinary("iptables") {
		_ = run("iptables", "-D", "FORWARD", "-i", cfg.TunName, "-j", "PPVPN")
		_ = run("iptables", "-D", "FORWARD", "-o", cfg.TunName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
		_ = run("iptables", "-F", "PPVPN")
		_ = run("iptables", "-X", "PPVPN")
		_ = run("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", cfg.ClientCIDR, "-o", wan, "-j", "MASQUERADE")
		return
	}
	if hasBinary("nft") {
		_ = run("nft", "delete", "table", "ip", nftTable)
		_ = run("nft", "delete", "table", "ip", nftNATTable)
	}
}

func configureIPTables(cfg Config, wan string) error {
	_ = run("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", cfg.ClientCIDR, "-o", wan, "-j", "MASQUERADE")
	if err := run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", cfg.ClientCIDR, "-o", wan, "-j", "MASQUERADE"); err != nil {
		return err
	}
	_ = run("iptables", "-N", "PPVPN")
	_ = run("iptables", "-F", "PPVPN")
	for _, c := range privateCIDRs {
		_ = run("iptables", "-A", "PPVPN", "-d", c, "-j", "DROP")
	}
	_ = run("iptables", "-A", "PPVPN", "-o", wan, "-j", "ACCEPT")
	_ = run("iptables", "-A", "PPVPN", "-j", "DROP")
	_ = run("iptables", "-C", "FORWARD", "-i", cfg.TunName, "-j", "PPVPN")
	if err := run("iptables", "-I", "FORWARD", "-i", cfg.TunName, "-j", "PPVPN"); err != nil {
		return err
	}
	_ = run("iptables", "-C", "FORWARD", "-o", cfg.TunName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	_ = run("iptables", "-I", "FORWARD", "-o", cfg.TunName, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	return nil
}

var privateCIDRs = []string{
	"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
	"100.64.0.0/10", "169.254.0.0/16", "127.0.0.0/8", "224.0.0.0/4",
}

// configureNFT 用 nftables 实现同样的 NAT + 隔离，表名独立，退出时整表删除。
func configureNFT(cfg Config, wan string) error {
	_ = run("nft", "delete", "table", "ip", nftTable)
	_ = run("nft", "delete", "table", "ip", nftNATTable)
	if err := run("nft", "add", "table", "ip", nftTable); err != nil {
		return err
	}
	if err := run("nft", "add", "chain", "ip", nftTable, "forward",
		"{ type filter hook forward priority 0; policy accept; }"); err != nil {
		return err
	}
	set := "{ " + strings.Join(privateCIDRs, ", ") + " }"
	if err := run("nft", "add", "rule", "ip", nftTable, "forward",
		"iifname", strconv.Quote(cfg.TunName), "ip", "daddr", set, "drop"); err != nil {
		return err
	}
	if err := run("nft", "add", "rule", "ip", nftTable, "forward",
		"iifname", strconv.Quote(cfg.TunName), "oifname", strconv.Quote(wan), "accept"); err != nil {
		return err
	}
	if err := run("nft", "add", "rule", "ip", nftTable, "forward",
		"iifname", strconv.Quote(cfg.TunName), "drop"); err != nil {
		return err
	}
	if err := run("nft", "add", "rule", "ip", nftTable, "forward",
		"oifname", strconv.Quote(cfg.TunName), "ct", "state", "related,established", "accept"); err != nil {
		return err
	}

	if err := run("nft", "add", "table", "ip", nftNATTable); err != nil {
		return err
	}
	if err := run("nft", "add", "chain", "ip", nftNATTable, "postrouting",
		"{ type nat hook postrouting priority 100; }"); err != nil {
		return err
	}
	if err := run("nft", "add", "rule", "ip", nftNATTable, "postrouting",
		"ip", "saddr", cfg.ClientCIDR, "oifname", strconv.Quote(wan), "masquerade"); err != nil {
		return err
	}
	return nil
}

func hasBinary(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func defaultWAN() string {
	out, err := exec.Command("sh", "-c", "ip route show default | awk '{print $5; exit}'").Output()
	if err != nil {
		return "eth0"
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "eth0"
	}
	return s
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
