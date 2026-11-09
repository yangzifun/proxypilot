package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"time"
)

// 隧道连通性探测：绑定隧道地址发一个真实 UDP DNS 查询，
// 经 OS 路由（0.0.0.0/1 → 虚拟网卡）走到节点，再走 NAT 出公网。
// 能收到应答就说明「客户端 → 隧道 → 节点 NAT → 公网 → 回程」整条链路是通的。

func probeTunnelOnce(localIP, dnsAddr string, timeout time.Duration) (time.Duration, error) {
	host, port, err := net.SplitHostPort(dnsAddr)
	if err != nil {
		return 0, fmt.Errorf("DNS 地址无效：%w", err)
	}
	src := net.ParseIP(localIP).To4()
	dst := net.ParseIP(host).To4()
	if src == nil || dst == nil {
		return 0, fmt.Errorf("地址无效（src=%s dst=%s）", localIP, host)
	}
	if _, err := net.ResolveUDPAddr("udp4", dnsAddr); err != nil {
		return 0, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: src})
	if err != nil {
		return 0, fmt.Errorf("绑定 %s 失败：%w", localIP, err)
	}
	defer conn.Close()

	q := buildDNSQuery(src, dst, 0x5a5a)
	start := time.Now()
	if _, err := conn.WriteToUDP(q, &net.UDPAddr{IP: dst, Port: atoiPort(port, 53)}); err != nil {
		return 0, fmt.Errorf("发送失败：%w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return 0, fmt.Errorf("无应答（%s）", err.Error())
	}
	if n < 12 || binary.BigEndian.Uint16(buf[0:2]) != 0x5a5a {
		return 0, fmt.Errorf("应答不匹配")
	}
	return time.Since(start), nil
}

func atoiPort(s string, def int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 || n > 65535 {
		return def
	}
	return n
}

// runTunnelProbe 在隧道建立后做一次连通性探测，结果通过回调返回。
func runTunnelProbe(ctx context.Context, localIP, dnsAddr string, done func(ok bool, rtt time.Duration, errMsg string)) {
	go func() {
		// 给路由/网卡一点生效时间
		select {
		case <-ctx.Done():
			return
		case <-time.After(1200 * time.Millisecond):
		}
		var lastErr error
		for attempt := 1; attempt <= 3; attempt++ {
			rtt, err := probeTunnelOnce(localIP, dnsAddr, 4*time.Second)
			if err == nil {
				log.Printf("隧道连通性：正常（%s，第 %d 次）", rtt.Round(time.Millisecond), attempt)
				done(true, rtt, "")
				return
			}
			lastErr = err
			log.Printf("隧道连通性：第 %d 次探测失败：%v", attempt, err)
			if ctx.Err() != nil {
				return
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("隧道连通性：不通（%v）", lastErr)
		done(false, 0, lastErr.Error())
	}()
}