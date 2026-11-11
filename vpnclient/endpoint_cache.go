package main

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// 节点地址缓存：把 hostname 解析结果记下来，连不上时用它兜底。
//
// 背景（真实故障）：客户端在隧道运行时会把系统 DNS 指向隧道网关
// （10.88.3.x）。断开后如果 DNS 还没恢复，下一次连接时
// quic.DialAddr 需要解析节点域名，此时系统解析器指向的是已经关掉的
// 网关，于是直接失败：
//
//	HTTP/3 鉴权失败：lookup lax1-node.yzfn.top: no such host
//
// 而同一时刻用 8.8.8.8 手动查询是能解析的 —— 说明不是域名问题，
// 是「在 DNS 不可用的时间窗里去解析」。
//
// 对策：连接开始前（DNS 还没被接管时）先解析并记住 IP；
// 之后 Dial 一律优先用记住的 IP，不再依赖当时的系统解析器。
var (
	epMu    sync.Mutex
	epCache = map[string]string{}
)

// rememberEndpointIP 解析并缓存 hostname 的 IP。失败不报错（不影响主流程）。
func rememberEndpointIP(host string) {
	host = strings.TrimSpace(host)
	if host == "" || net.ParseIP(host) != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil || len(addrs) == 0 {
		// 系统 DNS 不可用时（典型：上次异常退出把 DNS 还指向已关闭的隧道网关），
		// 直接问公共 DNS。这一步是「连接失败导致网络不可用」的最后一道兜底：
		// 只要物理链路通，就一定能解析出节点 IP，从而还能连上、还能自愈。
		for _, srv := range []string{"223.5.5.5:53", "1.1.1.1:53", "8.8.8.8:53"} {
			r := &net.Resolver{
				PreferGo: true,
				Dial: func(c context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{Timeout: 2 * time.Second}
					return d.DialContext(c, "udp", srv)
				},
			}
			if a, e := r.LookupHost(ctx, host); e == nil && len(a) > 0 {
				addrs = a
				break
			}
		}
	}
	if len(addrs) == 0 {
		return
	}
	// 优先 IPv4：隧道是 IPv4 的，IPv6 只有在有v6出口时才有意义
	pick := addrs[0]
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
			pick = a
			break
		}
	}
	epMu.Lock()
	epCache[host] = pick
	epMu.Unlock()
}

// cachedEndpointIP 返回该 hostname 上一次解析成功的 IP；没有则空。
func cachedEndpointIP(host string) string {
	epMu.Lock()
	defer epMu.Unlock()
	return epCache[host]
}

// dialAddrWithCache 把 "host:port" 里的 host 换成缓存 IP（若有），
// 这样 Dial 阶段不再触发系统 DNS 解析。
func dialAddrWithCache(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) != nil {
		return addr
	}
	if ip := cachedEndpointIP(host); ip != "" {
		return net.JoinHostPort(ip, port)
	}
	return addr
}
