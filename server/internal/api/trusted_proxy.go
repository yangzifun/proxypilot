package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
)

// =====================================================================
// 低-01：X-Forwarded-* / CF-Connecting-IP 只信任「可信反代」
//
// 之前 clientIP / requestOrigin / siteOrigin 无条件采信这些头，任何能直连源站的人
// 都能伪造来源 IP（绕过登录限速）或伪造 Host/Proto（污染安装命令、OIDC 回调地址）。
// 线上 80/443 虽只放行 Cloudflare 网段，但这是网络层兜底，代码层也应默认拒绝。
//
// 规则：只有当 TCP 对端（RemoteAddr）属于可信反代网段时才读取转发头；否则一律
// 使用连接本身的地址与请求 Host。可信网段默认 = 内置 Cloudflare 出网段 + 回环，
// 可用 PP_TRUSTED_PROXIES（逗号分隔 CIDR/IP）覆盖。
// =====================================================================

var (
	trustedProxyMu   sync.RWMutex
	trustedProxyNets []*net.IPNet
	// sitePublicOrigin 为配置的对外基址（site_public_base_url/PP_PUBLIC_BASE_URL）。
	// 非空时 requestOrigin/siteOrigin 直接使用它，不再从请求头推导。
	sitePublicOrigin string
)

// defaultTrustedProxies 是 Cloudflare 官方公布的出网段 + 回环地址。
// 线上 iptables 也只放行这些网段访问 80/443，二者保持一致。
var defaultTrustedProxies = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
	"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
	"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
	"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
	"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	"127.0.0.0/8", "::1/128",
}

// init 默认信任内置 Cloudflare 网段 + 回环（New() 可被 PP_TRUSTED_PROXIES 覆盖）。
func init() { configureTrustedProxies(nil) }

// configureTrustedProxies 在服务启动时由 New() 调用；list 为空时使用内置默认值。
func configureTrustedProxies(list []string) {
	raw := list
	if len(raw) == 0 {
		raw = defaultTrustedProxies
	}
	nets := make([]*net.IPNet, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			if _, n, err := net.ParseCIDR(item); err == nil {
				nets = append(nets, n)
			}
			continue
		}
		if ip := net.ParseIP(item); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	trustedProxyMu.Lock()
	trustedProxyNets = nets
	trustedProxyMu.Unlock()
}

func configureSitePublicOrigin(origin string) {
	trustedProxyMu.Lock()
	sitePublicOrigin = strings.TrimRight(strings.TrimSpace(origin), "/")
	trustedProxyMu.Unlock()
}

func configuredSiteOrigin() string {
	trustedProxyMu.RLock()
	defer trustedProxyMu.RUnlock()
	return sitePublicOrigin
}

// remoteAddrIP 取 TCP 对端 IP（不读任何转发头）。
func remoteAddrIP(r *http.Request) net.IP {
	if r == nil {
		return nil
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

// isTrustedProxyRequest 判断请求是否来自可信反代（只有这种请求的转发头才可信）。
func isTrustedProxyRequest(r *http.Request) bool {
	ip := remoteAddrIP(r)
	if ip == nil {
		return false
	}
	trustedProxyMu.RLock()
	nets := trustedProxyNets
	trustedProxyMu.RUnlock()
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// firstForwardedValue 取逗号分隔头的第一个值（Cloudflare 会覆盖并追加）。
func firstForwardedValue(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}