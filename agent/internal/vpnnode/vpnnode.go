// Package vpnnode 让 Agent 进程内直接承载 MASQUE VPN 节点（以及后续的组网管理）。
package vpnnode

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/yzfn/proxypilot/masque/node"
)

// DefaultConfigPath 是 Agent 内置 VPN 节点配置；不存在则不启用。
const DefaultConfigPath = "/etc/proxypilot/agent/vpn.json"

// Manager 管理进程内 VPN 节点的生命周期。
type Manager struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	active bool
}

// Start 读取配置并启动内嵌节点；配置文件不存在时返回未启用的 Manager。
func Start(ctx context.Context, cfgPath string, logf func(format string, args ...any)) (*Manager, error) {
	m := &Manager{}
	if cfgPath == "" {
		cfgPath = DefaultConfigPath
	}
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			logf("[vpn] 未找到 %s，跳过内嵌 VPN 节点", cfgPath)
			return m, nil
		}
		return m, err
	}
	cfg, err := node.LoadConfig(cfgPath)
	if err != nil {
		return m, err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv, err := node.New(cfg, logger)
	if err != nil {
		return m, err
	}

	vctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done, m.active = cancel, done, true
	m.mu.Unlock()

	go func() {
		defer close(done)
		if err := srv.Run(vctx); err != nil {
			logf("[vpn] 内嵌节点退出：%v", err)
		} else {
			logf("[vpn] 内嵌节点已停止")
		}
	}()
	logf("[vpn] 内嵌节点已启动：listen=%s tun=%s client_cidr=%s", cfg.Listen, cfg.TunName, cfg.ClientCIDR)
	return m, nil
}

// Active 返回节点是否在运行。
func (m *Manager) Active() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}

// Stop 停止内嵌节点并等待退出。
func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.active = false
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}
