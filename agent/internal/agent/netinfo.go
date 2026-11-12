package agent

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 公网 IP 探测：节点经内网直连面板时，面板看到的来源地址是内网/WARP 地址，
// 无法代表公网出口，所以由 Agent 主动查询并上报。
var publicIPEndpoints = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://ipinfo.io/ip",
	"https://api64.ipify.org",
}

type ipCache struct {
	mu   sync.Mutex
	val  string
	at   time.Time
	ttl  time.Duration
	busy bool
}

func (c *ipCache) get() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val
}

func (c *ipCache) fresh() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.val != "" && time.Since(c.at) < c.ttl
}

func (c *ipCache) set(v string) {
	c.mu.Lock()
	c.val = v
	c.at = time.Now()
	c.mu.Unlock()
}

var (
	agentPublicIP  = &ipCache{ttl: 10 * time.Minute}
	agentPrivateIP = &ipCache{ttl: 2 * time.Minute}
)

// detectPublicIP 依次尝试公共回显服务，返回第一个合法的公网 IP。
func detectPublicIP(ctx context.Context) string {
	cl := &http.Client{Timeout: 4 * time.Second}
	for _, ep := range publicIPEndpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep, nil)
		if err != nil {
			continue
		}
		resp, err := cl.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 128))
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			continue
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

// meshIfaceHints 组网网卡的常见名称（Cloudflare WARP / wgcf / tailscale / wireguard）。
// 与 metrics 包 net.go 的 meshIfaceNameHints 保持同一口径（两个包各有用途，故各留一份）。
var meshIfaceHints = []string{"cloudflarewarp", "warp", "wgcf", "tailscale", "wireguard"}

// isMeshIface 判断网卡是否为 WARP/组网隧道网卡。
// 除关键字外，还兼容 wireguard 的默认命名 wg0 / wg1 等。
func isMeshIface(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, hint := range meshIfaceHints {
		if strings.Contains(n, hint) {
			return true
		}
	}
	if len(n) >= 3 && strings.HasPrefix(n, "wg") && n[2] >= '0' && n[2] <= '9' {
		return true
	}
	return false
}

// detectPrivateIPv4 返回本机第一个内网 IPv4（10/172.16/192.168/CGNAT）。
//
// 跳过 WARP/组网网卡：WARP 会分配 100.64/10（或 172.16.0.2）组网地址，
// 那不是主机的内网 IP，报了会让面板把它当成主机地址显示。
func detectPrivateIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
			continue
		}
		if isMeshIface(it.Name) {
			continue
		}
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok || ipnet.IP.To4() == nil {
				continue
			}
			ip := ipnet.IP.String()
			if isPrivateV4(ip) {
				return ip
			}
		}
	}
	return ""
}

func isPrivateV4(s string) bool {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	return v4.IsPrivate() || // 10/8, 172.16/12, 192.168/16
		v4.IsLoopback() ||
		v4.IsLinkLocalUnicast() ||
		(v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127) // CGNAT 100.64/10（WARP 组网）
}

// currentPublicIP 读取缓存，过期时后台刷新，避免阻塞 poll。
func (a *Agent) currentPublicIP(ctx context.Context) string {
	if agentPublicIP.fresh() {
		return agentPublicIP.get()
	}
	go func() {
		if ip := detectPublicIP(context.Background()); ip != "" {
			agentPublicIP.set(ip)
		}
	}()
	return agentPublicIP.get()
}

// currentPrivateIP 读取本机内网 IP（变化不频繁，2 分钟内复用）。
func (a *Agent) currentPrivateIP() string {
	if agentPrivateIP.fresh() {
		return agentPrivateIP.get()
	}
	if ip := detectPrivateIPv4(); ip != "" {
		agentPrivateIP.set(ip)
	}
	return agentPrivateIP.get()
}

// detectMeshIPs 返回组网网卡上的 IPv4 / IPv6（Cloudflare WARP 同时会分配两者）。
func detectMeshIPs() (string, string) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", ""
	}
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || !isMeshIface(it.Name) {
			continue
		}
		addrs, err := it.Addrs()
		if err != nil {
			continue
		}
		var v4, v6 string
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP
			if ip4 := ip.To4(); ip4 != nil {
				if v4 == "" && !ip4.IsLoopback() && !ip4.IsLinkLocalUnicast() {
					v4 = ip4.String()
				}
				continue
			}
			if ip.To16() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				if v6 == "" {
					v6 = ip.String()
				}
			}
		}
		if v4 != "" || v6 != "" {
			return v4, v6
		}
	}
	return "", ""
}

var (
	mesh4Cache = &ipCache{ttl: 15 * time.Second}
	mesh6Cache = &ipCache{ttl: 15 * time.Second}
)

// currentMeshIPs 返回组网 IPv4（带缓存）。
func (a *Agent) currentMeshIPs() string {
	if mesh4Cache.fresh() {
		return mesh4Cache.get()
	}
	v4, v6 := detectMeshIPs()
	if v4 != "" {
		mesh4Cache.set(v4)
	}
	if v6 != "" {
		mesh6Cache.set(v6)
	}
	return v4
}

// currentMeshIP6 返回组网 IPv6（带缓存）。
func (a *Agent) currentMeshIP6() string {
	if mesh6Cache.fresh() {
		return mesh6Cache.get()
	}
	v4, v6 := detectMeshIPs()
	if v4 != "" {
		mesh4Cache.set(v4)
	}
	if v6 != "" {
		mesh6Cache.set(v6)
	}
	return v6
}
