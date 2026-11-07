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

// detectPrivateIPv4 返回本机第一个内网 IPv4（10/172.16/192.168/CGNAT）。
func detectPrivateIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, it := range ifaces {
		if it.Flags&net.FlagUp == 0 || it.Flags&net.FlagLoopback != 0 {
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
	return v4.IsPrivate() ||     // 10/8, 172.16/12, 192.168/16
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
