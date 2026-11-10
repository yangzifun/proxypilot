//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// 断线保护（Kill Switch）。
//
// 目标：连接期间，除「隧道内流量」与「到节点端点的 QUIC」以外，禁止任何出站，
// 防止用户在隧道中断的瞬间裸奔出去。
//
// 实现要点：Windows 防火墙的 Block 规则优先级高于 Allow，无法用「阻断物理网卡
// + 放行端点」实现；正确的做法是把三个 profile 的默认出站动作改为 Block，
// 再用 Allow 规则白名单放行：
//
//	放行 127.0.0.0/8、::1       本地控制台
//	放行 隧道网卡接口             所有走 VPN 的应用流量
//	放行 节点端点 IP/32           QUIC 承载本身
//	放行 UDP 67/68               DHCP 续约，避免断网后拿不到地址
//
// 断开/退出/崩溃恢复时把默认动作还原为 Allow 并删除白名单规则。
// 因为是系统性改动，功能默认关闭，由用户在设置页显式开启。

const killSwitchTag = "ProxyPilot-KS"

func killSwitchMarkerPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ppvpn", "killswitch.json")
}

type ksState struct {
	Profiles  map[string]string `json:"profiles"` // profile -> 原 DefaultOutboundAction
	Rules     []string          `json:"rules"`
	Endpoints []string          `json:"endpoints"`
	TunAlias  string            `json:"tun_alias"`
}

// enableKillSwitch 开启断线保护，返回已下发的规则名。
func enableKillSwitch(endpoints []string, tunAlias string) ([]string, error) {
	if tunAlias == "" {
		return nil, fmt.Errorf("隧道网卡名为空，跳过断线保护")
	}

	prev := readKillSwitchState()
	if prev == nil {
		prev = &ksState{Profiles: map[string]string{}}
	}

	// 1) 记录三个 profile 当前的默认出站动作（只在首次记录，避免覆盖原值）
	for _, p := range []string{"Domain", "Private", "Public"} {
		if _, ok := prev.Profiles[p]; ok {
			continue
		}
		out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
			fmt.Sprintf(`(Get-NetFirewallProfile -Name %s).DefaultOutboundAction`, p))
		if err != nil {
			return nil, fmt.Errorf("读取 %s profile 失败：%v", p, err)
		}
		prev.Profiles[p] = strings.TrimSpace(out)
	}
	prev.TunAlias = tunAlias
	prev.Endpoints = endpoints
	writeKillSwitchState(prev)

	// 2) 白名单规则（幂等：先删同名再加）
	removeKillSwitchRules()

	var applied []string
	addRule := func(name, args string) bool {
		ps := fmt.Sprintf(`New-NetFirewallRule -DisplayName '%s-%s' %s -ErrorAction Stop | Out-Null`, killSwitchTag, name, args)
		if out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command", ps); err != nil {
			log.Printf("断线保护：添加规则 %s 失败：%v %s", name, err, strings.TrimSpace(out))
			return false
		}
		applied = append(applied, killSwitchTag+"-"+name)
		return true
	}

	addRule("loopback", `-Direction Outbound -Action Allow -RemoteAddress 127.0.0.0/8,::1`)
	addRule("tun", fmt.Sprintf(`-Direction Outbound -Action Allow -InterfaceAlias '%s'`, tunAlias))
	for _, ip := range endpoints {
		if isIPv4(ip) {
			addRule("ep-"+strings.ReplaceAll(ip, ".", "_"),
				fmt.Sprintf(`-Direction Outbound -Action Allow -Protocol Any -RemoteAddress %s/32`, ip))
		}
	}
	addRule("dhcp", `-Direction Outbound -Action Allow -Protocol UDP -RemotePort 67 -RemoteAddress 255.255.255.255`)

	// 3) 最后才切换默认动作，失败立即回滚
	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
		`Set-NetFirewallProfile -Profile Domain,Private,Public -DefaultOutboundAction Block`)
	if err != nil {
		log.Printf("断线保护：切换默认出站为阻断失败：%v %s", err, strings.TrimSpace(out))
		restoreKillSwitch()
		return nil, fmt.Errorf("无法启用断线保护：%v", err)
	}

	prev.Rules = applied
	writeKillSwitchState(prev)
	log.Printf("断线保护：已开启（默认阻断出站，白名单 %d 条，隧道网卡 %s）", len(applied), tunAlias)
	return applied, nil
}

// disableKillSwitch 关闭断线保护并还原默认动作。
func disableKillSwitch() {
	restoreKillSwitch()
}

// restoreKillSwitch 还原默认出站动作并删除白名单规则（幂等）。
func restoreKillSwitch() {
	st := readKillSwitchState()
	removeKillSwitchRules()

	// 还原默认动作；没有记录就还原为 Allow（Windows 出厂默认）
	domain, private, public := "Allow", "Allow", "Allow"
	if st != nil {
		if v := st.Profiles["Domain"]; v != "" {
			domain = v
		}
		if v := st.Profiles["Private"]; v != "" {
			private = v
		}
		if v := st.Profiles["Public"]; v != "" {
			public = v
		}
	}
	out, err := runWin("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf(`Set-NetFirewallProfile -Profile Domain,Private,Public -DefaultOutboundAction %s,%s,%s`,
			domain, private, public))
	if err != nil {
		log.Printf("断线保护：还原默认出站动作失败（请手动检查防火墙）：%v %s", err, strings.TrimSpace(out))
	}
	removeKillSwitchState()
}

// restoreKillSwitchFromMarker 启动时调用：上次异常退出可能留下阻断状态。
func restoreKillSwitchFromMarker() {
	if _, err := os.Stat(killSwitchMarkerPath()); err != nil {
		return
	}
	log.Printf("断线保护：检测到上次未还原的阻断状态，正在恢复")
	restoreKillSwitch()
}

func removeKillSwitchRules() {
	runWinSilent("powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf(`Get-NetFirewallRule -DisplayName '%s-*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue`, killSwitchTag))
}

func readKillSwitchState() *ksState {
	data, err := os.ReadFile(killSwitchMarkerPath())
	if err != nil {
		return nil
	}
	var st ksState
	if json.Unmarshal(data, &st) != nil {
		return nil
	}
	return &st
}

func writeKillSwitchState(st *ksState) {
	path := killSwitchMarkerPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	raw, _ := json.MarshalIndent(st, "", "  ")
	_ = os.WriteFile(path, raw, 0o600)
}

func removeKillSwitchState() {
	_ = os.Remove(killSwitchMarkerPath())
}