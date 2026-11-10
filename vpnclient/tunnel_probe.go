package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// 隧道连通性探测。
//
// 历史问题：
//   1. 只打单个 UDP DNS 目标 —— 目标挂了就误判整条隧道不通；
//   2. 串行 + 长超时 —— 5 个目标 × 3s × 3 轮，界面要等几十秒才出结果；
//   3. 只用 UDP:53 —— 客户端自己的 DNS 守卫会封物理网卡的 53，
//      一旦路由还没完全生效，探测包直接被挡，于是「实际能用但显示不通」。
//
// 现在：UDP DNS 与 TCP:443 两种方法**并行**跑，任一成功即判定连通，
// 且优先给出「哪个目标、哪种方法、多少毫秒」。

type probeTarget struct {
	Name string
	Addr string
}

// probeTargets 生成探测目标：公共 DNS + 一个 TCP 目标。
// 不再把节点网关 :53 当必选目标 —— 节点上并不一定有 DNS 监听（实测没有），
// 放进去只会白等一个超时。
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
	add("Cloudflare", "1.1.1.1:53")
	add("AliDNS", "223.5.5.5:53")
	add("Quad9", "9.9.9.9:53")
	if dnsAddr != "" {
		add("自定义 DNS", dnsAddr)
	}
	if gw := strings.TrimSpace(gateway); gw != "" {
		add("节点网关", net.JoinHostPort(gw, "53"))
	}
	return out
}

// probeTCPTargets TCP 目标：不碰 53 端口，绕开 DNS 守卫的干扰。
func probeTCPTargets() []probeTarget {
	return []probeTarget{
		{Name: "Cloudflare TCP", Addr: "1.1.1.1:443"},
		{Name: "AliDNS TCP", Addr: "223.5.5.5:443"},
	}
}

type probeResult struct {
	ok   bool
	rtt  time.Duration
	info string
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

// probeTCPOnce 用 TCP 握手探测：源地址绑到隧道 IP，确保走隧道出去。
func probeTCPOnce(localIP, addr string, timeout time.Duration) (time.Duration, error) {
	src := net.ParseIP(localIP).To4()
	if src == nil {
		return 0, fmt.Errorf("源地址无效：%s", localIP)
	}
	d := net.Dialer{
		Timeout:   timeout,
		LocalAddr: &net.TCPAddr{IP: src},
	}
	start := time.Now()
	conn, err := d.Dial("tcp4", addr)
	if err != nil {
		return 0, err
	}
	rtt := time.Since(start)
	_ = conn.Close()
	return rtt, nil
}

// runTunnelProbe 并行探测，第一个成功的结果立刻返回。
// 全部失败时把每个目标的错误拼起来（界面 title 里能看到）。
func runTunnelProbe(ctx context.Context, localIP, dnsAddr, gateway string, done func(ok bool, rtt time.Duration, info string)) {
	go func() {
		// 给路由/网卡一点生效时间
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}

		udpTargets := probeTargets(gateway, dnsAddr)
		tcpTargets := probeTCPTargets()

		var (
			mu       sync.Mutex
			errs     []string
			finished bool
		)

		perTry := 2 * time.Second
		rounds := 3

		for round := 1; round <= rounds; round++ {
			if ctx.Err() != nil {
				return
			}
			resCh := make(chan probeResult, len(udpTargets)+len(tcpTargets))

			for _, t := range udpTargets {
				t := t
				go func() {
					rtt, err := probeTunnelOnce(localIP, t.Addr, perTry)
					if err != nil {
						mu.Lock()
						errs = append(errs, fmt.Sprintf("%s(%s)：%v", t.Name, t.Addr, err))
						mu.Unlock()
						resCh <- probeResult{}
						return
					}
					resCh <- probeResult{ok: true, rtt: rtt, info: fmt.Sprintf("%s %s", t.Name, t.Addr)}
				}()
			}
			for _, t := range tcpTargets {
				t := t
				go func() {
					rtt, err := probeTCPOnce(localIP, t.Addr, perTry)
					if err != nil {
						mu.Lock()
						errs = append(errs, fmt.Sprintf("%s(%s)：%v", t.Name, t.Addr, err))
						mu.Unlock()
						resCh <- probeResult{}
						return
					}
					resCh <- probeResult{ok: true, rtt: rtt, info: fmt.Sprintf("%s %s", t.Name, t.Addr)}
				}()
			}

			// 收本轮结果：谁先成功用谁
			for i := 0; i < len(udpTargets)+len(tcpTargets); i++ {
				r := <-resCh
				if r.ok && !finished {
					finished = true
					ms := r.rtt.Round(time.Millisecond)
					log.Printf("隧道连通性：正常（%s，%s，第 %d 轮）", r.info, ms, round)
					done(true, r.rtt, fmt.Sprintf("%s · %s", r.info, ms))
					return
				}
			}

			log.Printf("隧道连通性：第 %d 轮全部失败", round)
			if round < rounds {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}

		mu.Lock()
		detail := strings.Join(errs, "；")
		mu.Unlock()
		if len(detail) > 300 {
			detail = detail[:300] + "…"
		}
		log.Printf("隧道连通性：不通（%s）", detail)
		done(false, 0, detail)
	}()
}

// buildDNSMessage 构造一个标准 DNS 查询报文（只含 DNS 消息，IP/UDP 头交给内核）。
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