// ProxyPilot VPN 客户端 1.0（联调用 CLI）
//
// 流程：门户令牌 → 已绑定节点 → /api/portal/vpn/connect 换取连接 JWT →
// HTTP/3 鉴权 → QUIC DATAGRAM 发送 DNS 探测包，验证节点数据面。
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"github.com/songgao/water"
)

// Version 是 VPN 客户端版本（V1.x）。
const Version = "1.0.0"

type node struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Region   string `json:"region"`
	Endpoint string `json:"endpoint"`
	Online   bool   `json:"online"`
}

type profile struct {
	SessionID int64 `json:"session_id"`
	Token     string
	IP4       string `json:"ip4"`
	MTU       int    `json:"mtu"`
	DNS       []string
	Node      struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Endpoint string `json:"endpoint"`
	} `json:"node"`
	TLS struct {
		ServerName string `json:"server_name"`
		Mode       string `json:"mode"`
	} `json:"tls"`
}

func main() {
	apiBase := flag.String("api", "https://proxypilot.yangzifun.org", "面板地址")
	token := flag.String("token", "", "门户登录令牌（Bearer）")
	deviceID := flag.Int64("device", 0, "设备 ID")
	nodeID := flag.Int64("node", 0, "节点 ID（0=自动选第一个在线节点）")
	insecure := flag.Bool("insecure", false, "跳过 TLS 证书校验（自签节点）")
	dnsAddr := flag.String("dns", "1.1.1.1:53", "探测用 DNS（经隧道访问）")
	meshToken := flag.String("mesh-token", "", "组网 JWT（设置后进入组网模式）")
	meshIP := flag.String("mesh-ip", "", "组网 IP，例如 10.99.0.11")
	meshCIDR := flag.String("mesh-cidr", "10.99.0.0/16", "组网网段")
	meshTun := flag.String("mesh-tun", "ppmesh0", "组网 TUN 名称")
	meshEndpoint := flag.String("mesh-endpoint", "vpncenter.yzfn.top:443", "组网中心地址")
	meshSNI := flag.String("mesh-sni", "vpncenter.yzfn.top", "组网中心 SNI")
	timeout := flag.Duration("timeout", 20*time.Second, "整体超时")
	flag.Parse()
	log.Printf("ProxyPilot VPN Client %s", Version)

	if *meshToken != "" {
		if *meshIP == "" {
			log.Fatal("组网模式需要 -mesh-ip")
		}
		mctx, mcancel := context.WithCancel(context.Background())
		defer mcancel()
		if err := runMesh(mctx, *meshToken, *meshIP, *meshCIDR, *meshTun, *meshEndpoint, *meshSNI, *insecure); err != nil {
			log.Fatalf("组网失败：%v", err)
		}
		return
	}

	if *token == "" || *deviceID <= 0 {
		log.Fatal("需要 -token（门户令牌）与 -device（设备 ID）")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	api := strings.TrimRight(*apiBase, "/")

	nodes, err := listNodes(ctx, api, *token)
	if err != nil {
		log.Fatalf("拉取节点失败：%v", err)
	}
	if len(nodes) == 0 {
		log.Fatal("没有可用节点（未绑定或资格无效）")
	}
	pick := nodes[0]
	for _, n := range nodes {
		if *nodeID > 0 && n.ID == *nodeID {
			pick = n
		}
	}
	log.Printf("选择节点 #%d %s endpoint=%s online=%v", pick.ID, pick.Name, pick.Endpoint, pick.Online)

	prof, err := connect(ctx, api, *token, *deviceID, pick.ID)
	if err != nil {
		log.Fatalf("连接接口失败：%v", err)
	}
	sni := prof.TLS.ServerName
	if sni == "" {
		sni = strings.Split(pick.Endpoint, ":")[0]
	}
	log.Printf("连接成功：session=%d ip4=%s sni=%s mtu=%d", prof.SessionID, prof.IP4, sni, prof.MTU)

	assigned, conn, err := authHTTP3(ctx, pick.Endpoint, sni, prof.Token, *insecure)
	if err != nil {
		log.Fatalf("HTTP/3 鉴权失败：%v", err)
	}
	log.Printf("HTTP/3 鉴权成功，节点确认客户端 IP=%s", assigned)
	defer conn.CloseWithError(0, "done")

	resp, err := dnsProbe(ctx, conn, net.ParseIP(prof.IP4).To4(), *dnsAddr)
	if err != nil {
		log.Fatalf("DNS 探测失败：%v", err)
	}
	log.Printf("DNS 探测成功：收到 %d 字节响应（%s）", len(resp), *dnsAddr)
}

func listNodes(ctx context.Context, api, token string) ([]node, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, api+"/api/portal/vpn/nodes", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Nodes []node `json:"nodes"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out.Nodes, nil
}

func connect(ctx context.Context, api, token string, deviceID, nodeID int64) (*profile, error) {
	body := fmt.Sprintf(`{"device_id":%d,"node_id":%d}`, deviceID, nodeID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/portal/vpn/connect", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}
	var p profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func runMesh(ctx context.Context, token, ip, cidr, tunName, endpoint, sni string, insecure bool) error {
	dev, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{Name: tunName},
	})
	if err != nil {
		return fmt.Errorf("创建 TUN: %w", err)
	}
	defer dev.Close()
	if err := exec.Command("ip", "addr", "add", ip+"/"+strings.Split(cidr, "/")[1], "dev", dev.Name()).Run(); err != nil {
		return fmt.Errorf("配置地址: %w", err)
	}
	if err := exec.Command("ip", "link", "set", "dev", dev.Name(), "up").Run(); err != nil {
		return fmt.Errorf("启用 TUN: %w", err)
	}
	assigned, conn, err := authHTTP3(ctx, endpoint, sni, token, insecure)
	if err != nil {
		return fmt.Errorf("HTTP/3 鉴权: %w", err)
	}
	log.Printf("组网鉴权成功：节点分配 %s（本地 %s）", assigned, ip)

	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := dev.Read(buf)
			if err != nil {
				return
			}
			_ = conn.SendDatagram(buf[:n])
		}
	}()
	for {
		data, err := conn.ReceiveDatagram(ctx)
		if err != nil {
			return err
		}
		pkt, _, _, err := strip(data)
		if err != nil {
			continue
		}
		if _, err := dev.Write(pkt); err != nil {
			return err
		}
	}
}

func authHTTP3(ctx context.Context, endpoint, sni, token string, insecure bool) (string, *quic.Conn, error) {
	var conn *quic.Conn
	tlsConf := &tls.Config{
		ServerName:         sni,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h3"},
		InsecureSkipVerify: insecure,
	}
	tr := &http3.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig: &quic.Config{
			EnableDatagrams: true,
			KeepAlivePeriod: 10 * time.Second,
			MaxIdleTimeout:  60 * time.Second,
		},
		EnableDatagrams: true,
		Dial: func(ctx context.Context, addr string, tlsCfg *tls.Config, cfg *quic.Config) (*quic.Conn, error) {
			c, err := quic.DialAddr(ctx, addr, tlsCfg, cfg)
			if err == nil {
				conn = c
			}
			return c, err
		},
	}
	// 不在这里 Close：Transport 关闭会同时关闭底层 QUIC 连接，
	// 后续 DATAGRAM 探测需要复用同一条连接。进程退出时由内核回收。
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+endpoint+"/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-VPN-Auth"); got != "Success" {
		return "", nil, fmt.Errorf("X-VPN-Auth=%q status=%d", got, resp.StatusCode)
	}
	if conn == nil {
		return "", nil, fmt.Errorf("未捕获 QUIC 连接")
	}
	return resp.Header.Get("X-VPN-Assigned-IPv4"), conn, nil
}

func dnsProbe(ctx context.Context, conn *quic.Conn, src net.IP, dstAddr string) ([]byte, error) {
	host, _, err := net.SplitHostPort(dstAddr)
	if err != nil {
		return nil, err
	}
	dst := net.ParseIP(host).To4()
	if src == nil || dst == nil {
		return nil, fmt.Errorf("IPv4 地址无效")
	}
	pkt := buildDNSQuery(src, dst, 0x1234)
	if err := conn.SendDatagram(pkt); err != nil {
		return nil, err
	}
	dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		data, err := conn.ReceiveDatagram(dctx)
		if err != nil {
			return nil, err
		}
		p, _, _, err := strip(data)
		if err != nil || len(p) < 28 || p[9] != 17 {
			continue
		}
		udpSrc := binary.BigEndian.Uint16(p[20:22])
		if udpSrc != 53 {
			continue
		}
		return p, nil
	}
}

func buildDNSQuery(src, dst net.IP, id uint16) []byte {
	name := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	dns := make([]byte, 12+len(name)+4)
	binary.BigEndian.PutUint16(dns[0:2], id)
	dns[2] = 0x01 // recursion desired
	binary.BigEndian.PutUint16(dns[4:6], 1)
	copy(dns[12:], name)
	binary.BigEndian.PutUint16(dns[len(dns)-4:], 1) // A
	binary.BigEndian.PutUint16(dns[len(dns)-2:], 1) // IN

	udpLen := 8 + len(dns)
	total := 20 + udpLen
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64
	pkt[9] = 17
	copy(pkt[12:16], src)
	copy(pkt[16:20], dst)
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:20]))
	binary.BigEndian.PutUint16(pkt[20:22], 40000)
	binary.BigEndian.PutUint16(pkt[22:24], 53)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(udpLen))
	copy(pkt[28:], dns)
	return pkt
}

func checksum(b []byte) uint16 {
	sum := uint32(0)
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func strip(b []byte) ([]byte, net.IP, net.IP, error) {
	if len(b) < 20 || b[0]>>4 != 4 {
		return nil, nil, nil, fmt.Errorf("bad ipv4")
	}
	total := int(binary.BigEndian.Uint16(b[2:4]))
	if total < 20 || total > len(b) {
		return nil, nil, nil, fmt.Errorf("bad len")
	}
	return b[:total], net.IP(b[12:16]), net.IP(b[16:20]), nil
}



