package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// 隧道连通性探测：绑定隧道地址发一个真实 UDP DNS 查询，
// 经 OS 路由（0.0.0.0/1 → 虚拟网卡）走到节点，再走 NAT 出公网。
// 能收到应答就说明「客户端 → 隧道 → 节点 NAT → 公网 → 回程」整条链路是通的。
//
// 多目标版本：单目标失败无法区分「隧道不通」还是「那个 DNS 被墙/被拦」，
// 之前就因此一直显示「不通」。现在按 网关 → 公共 DNS 的顺序逐个试，
// 全部失败时把每个目标的错误都带回界面。

// probeTarget 一个探测目标
type probeTarget struct {
	Name string
	Addr string
}

// probeTargets 生成探测目标列表：先节点网关（经隧道最近、必经 NAT），
// 再几个不同运营商的公共 DNS，任何一个通就算数据面通。
func probeTargets(gateway, dnsAddr string) []probeTarget {
	out := []probeTarget{}
	seen := map[string]bool{}
	add := func(name, addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" || seen[addr] {
			return
		}
		seen[addr] = true
		out = append(out, probeTarget{Name: name, Addr: addr})
	}
	if gw := strings.TrimSpace(gateway); gw != "" {
		add("节点网关", net.JoinHostPort(gw, "53"))
	}
	add("Cloudflare", "1.1.1.1:53")
	add("AliDNS", "223.5.5.5:53")
	add("Quad9", "9.9.9.9:53")
	if dnsAddr != "" {
		add("自定义 DNS", dnsAddr)
	}
	return out
}

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

	const qid uint16 = 0x5a5a
	q := buildDNSMessage(qid)
	target := &net.UDPAddr{IP: dst, Port: atoiPort(port, 53)}
	start := time.Now()
	if _, err := conn.WriteToUDP(q, target); err != nil {
		return 0, fmt.Errorf("发送失败：%w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 1500)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return 0, fmt.Errorf("无应答（%s）", err.Error())
		}
		if n >= 12 && binary.BigEndian.Uint16(buf[0:2]) == qid {
			return time.Since(start), nil
		}
	}
}

// buildDNSMessage 构造一个标准 DNS 查询报文（只含 DNS 消息，IP/UDP 头交给内核）。
//
// 早期版本错误地把「手工拼好的 IPv4 包」当作 UDP 载荷发出去，
// 对端收到的是乱码，所以连通检测永远显示「不通」。
func buildDNSMessage(id uint16) []byte {
	name := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	msg := make([]byte, 12+len(name)+4)
	binary.BigEndian.PutUint16(msg[0:2], id)
	msg[2] = 0x01 // recursion desired
	binary.BigEndian.PutUint16(msg[4:6], 1)
	copy(msg[12:], name)
	binary.BigEndian.PutUint16(msg[len(msg)-4:], 1) // A
	binary.BigEndian.PutUint16(msg[len(msg)-2:], 1) // IN
	return msg
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

// runTunnelProbe 在隧道建立后做连通性探测，结果通过回调返回。
// gateway 可空；给了就优先探它。
func runTunnelProbe(ctx context.Context, localIP, dnsAddr, gateway string, done func(ok bool, rtt time.Duration, info string)) {
	go func() {
		// 给路由/网卡一点生效时间
		select {
		case <-ctx.Done():
			return
		case <-time.After(1200 * time.Millisecond):
		}

		targets := probeTargets(gateway, dnsAddr)
		for round := 1; round <= 3; round++ {
			errs := make([]string, 0, len(targets))
			for _, t := range targets {
				if ctx.Err() != nil {
					return
				}
				rtt, err := probeTunnelOnce(localIP, t.Addr, 3*time.Second)
				if err == nil {
					ms := rtt.Round(time.Millisecond)
					log.Printf("隧道连通性：正常（%s %s，%s，第 %d 轮）", t.Name, t.Addr, ms, round)
					done(true, rtt, fmt.Sprintf("%s %s · %s", t.Name, t.Addr, ms))
					return
				}
				errs = append(errs, fmt.Sprintf("%s(%s)：%v", t.Name, t.Addr, err))
				log.Printf("隧道连通性：%s 失败：%v", t.Addr, err)
			}
			log.Printf("隧道连通性：第 %d 轮全部失败", round)
			if round < 3 {
				time.Sleep(2 * time.Second)
			}
		}
		done(false, 0, strings.Join(targets2short(targets), "、")+" 均无应答")
		_ = localIP
	}()
}

func targets2short(ts []probeTarget) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}