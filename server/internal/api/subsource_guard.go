package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// 外部订阅源是「用户可控 URL → 服务端发起请求」的 SSRF 面：
// 只校验字面 IP 不够，攻击者可用十进制 IP、DNS rebinding、302 跳转绕过。
// 这里在真正拨号时重新解析并校验所有地址，且对每一跳重定向复用 URL 校验。
var errSubSourceBlocked = errors.New("订阅源地址不允许访问")

// isBlockedIP 判定地址是否属于不允许服务端主动访问的范围。
// 覆盖 loopback / 私网 / 链路本地 / CGNAT / 文档与保留网段 / 广播 / 组播。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0: // 0.0.0.0/8
			return true
		case v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127: // 100.64.0.0/10 CGNAT
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24
			return true
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // 192.0.2.0/24 TEST-NET-1
			return true
		case v4[0] == 198 && (v4[1] == 18 || v4[1] == 19): // 198.18.0.0/15 benchmark
			return true
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // 198.51.100.0/24 TEST-NET-2
			return true
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // 203.0.113.0/24 TEST-NET-3
			return true
		case v4[0] >= 240: // 240.0.0.0/4 + 255.255.255.255
			return true
		}
	}
	return false
}

// subSourceHTTPClient 返回带 SSRF 防护的客户端：
//   - 拨号前解析域名，拒绝任一解析到内网/保留地址的结果（防 DNS rebinding）；
//   - 只拨号已验证的 IP；
//   - 重定向逐跳校验，且最多 5 跳；
//   - 禁用环境代理，避免代理绕过地址校验。
func subSourceHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, errSubSourceBlocked
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, errSubSourceBlocked
			}
			for _, ip := range ips {
				if isBlockedIP(ip.IP) {
					return nil, errSubSourceBlocked
				}
			}
			dialer := &net.Dialer{Timeout: 8 * time.Second}
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			if lastErr == nil {
				lastErr = errSubSourceBlocked
			}
			return nil, lastErr
		},
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
	}
	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errSubSourceBlocked
			}
			if _, ok := validSubSourceURL(req.URL.String()); !ok {
				return errSubSourceBlocked
			}
			return nil
		},
	}
}
