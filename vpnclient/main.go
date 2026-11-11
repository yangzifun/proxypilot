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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// Version 是 VPN 客户端版本（V1.x）。
const Version = "3.11"

var caPEM []byte

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
	// 36.0 数据面双通道（面板可关，关闭即全部走可靠流）
	DatagramEnabled bool   `json:"datagram_enabled"`
	FECMode         string `json:"fec_mode"`
	FECGroup        int    `json:"fec_group"`
	DNS       []string
	// 服务端下发的组网网段与节点客户端子网（客户端不自填）
	MeshCIDR   string `json:"mesh_cidr"`
	ClientCIDR string `json:"client_cidr"`
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

func runCLI() {
	apiBase := flag.String("api", "https://proxypilot.yangzifun.org", "面板地址")
	token := flag.String("token", "", "门户登录令牌（Bearer）")
	deviceID := flag.Int64("device", 0, "设备 ID")
	nodeID := flag.Int64("node", 0, "节点 ID（0=自动选第一个在线节点）")
	insecure := flag.Bool("insecure", false, "跳过 TLS 证书校验（自签节点）")
	caPath := flag.String("ca", "", "内部 CA 证书路径（PEM），用于校验节点证书")
	dnsAddr := flag.String("dns", "1.1.1.1:53", "探测用 DNS（经隧道访问）")
	meshToken := flag.String("mesh-token", "", "组网 JWT（设置后进入组网模式）")
	meshIP := flag.String("mesh-ip", "", "组网 IP，例如 10.99.0.11")
	meshCIDR := flag.String("mesh-cidr", "10.99.0.0/16", "组网网段")
	meshTun := flag.String("mesh-tun", "ppmesh0", "组网 TUN 名称")
	meshEndpoint := flag.String("mesh-endpoint", "vpncenter.yzfn.top:443", "组网中心地址（可用逗号分隔多个候选，按序回退）")
	meshSNI := flag.String("mesh-sni", "vpncenter.yzfn.top", "组网中心 SNI")
	meshAPI := flag.String("mesh-api", "https://proxypilot.yangzifun.org", "组网心跳 API 地址")
	meshHeartbeat := flag.Duration("mesh-heartbeat", 20*time.Second, "组网心跳间隔")
	meshMode := flag.String("mesh-mode", "mesh", "接管范围：mesh=仅组网网段，full=接管全部流量")
	uiMode := flag.Bool("ui", false, "启动本地控制台界面（127.0.0.1）")
	uiAddr := flag.String("ui-addr", "127.0.0.1:8899", "控制台监听地址")
	uiOpen := flag.Bool("ui-open", true, "启动后自动打开浏览器")
	uiDemo := flag.Bool("ui-demo", false, "控制台使用演示数据（不发起真实连接）")
	tunSelftest := flag.Bool("tun-selftest", false, "自检：创建网卡并下发路由后立即清理（需要管理员/root）")
	doInstall := flag.Bool("install", false, "安装：释放驱动、注册虚拟网卡、写入卸载信息（需要管理员）")
	doUninstall := flag.Bool("uninstall", false, "卸载：移除虚拟网卡与卸载信息（需要管理员）")
	installDir := flag.String("install-dir", "", "安装目录（默认 %LOCALAPPDATA%\\Programs\\ProxyPilot）")
	installAutostart := flag.Bool("autostart", false, "安装时写入开机自启（托盘后台运行）")
	adapterCheck := flag.Bool("adapter-status", false, "检查虚拟网卡是否已注册")
	doDiag := flag.Bool("diag", false, "生成诊断报告（网卡、路由、日志路径）")
	tunSelftestMode := flag.String("tun-selftest-mode", "mesh", "自检接管模式：mesh|full")
	tunSelftestKeep := flag.Int("tun-selftest-keep", 5, "自检保留秒数（到点自动清理）")
	cfgPath := flag.String("config", "", "配置文件路径（默认用系统配置目录）")
	timeout := flag.Duration("timeout", 20*time.Second, "整体超时")
	flag.Parse()
	if *caPath != "" {
		b, err := os.ReadFile(*caPath)
		if err != nil {
			log.Fatalf("读取 CA 失败：%v", err)
		}
		caPEM = b
	}
	setupFileLog()
	cleanStaleTunnel()
	log.Printf("ProxyPilot VPN Client %s", Version)

	if *cfgPath != "" {
		configPathOverride = *cfgPath
	}
	if *doInstall {
		runInstallMode(*installDir, *installAutostart)
		return
	}
	if *doUninstall {
		runUninstallMode()
		return
	}
	if *adapterCheck {
		runAdapterStatus()
		return
	}
	if *doDiag {
		runDiag()
		return
	}

	if *tunSelftest {
		mode := TunMode(strings.ToLower(strings.TrimSpace(*tunSelftestMode)))
		if mode != TunModeFull {
			mode = TunModeMesh
		}
		if err := runTunSelftest(mode, *tunSelftestKeep); err != nil {
			log.Fatalf("网卡自检失败：%v", err)
		}
		return
	}

	if *uiMode {
		uctx, ustop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer ustop()
		defaults := uiSettings{
			API:      strings.TrimRight(*apiBase, "/"),
			Token:    *token,
			Insecure: *insecure,
			DNSAddr:  *dnsAddr,
			NodeID:   *nodeID,
		}
		if err := runUI(uctx, *uiAddr, defaults, *uiOpen, *uiDemo); err != nil {
			log.Fatalf("控制台启动失败：%v", err)
		}
		return
	}

	if *meshToken != "" {
		if *meshIP == "" {
			log.Fatal("组网模式需要 -mesh-ip")
		}
		mctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		eps := splitEndpoints(*meshEndpoint)
		mm := TunMode(strings.ToLower(strings.TrimSpace(*meshMode)))
		if mm != TunModeFull {
			mm = TunModeMesh
		}
		if err := runMesh(mctx, *meshToken, *meshIP, *meshCIDR, *meshTun, eps, *meshSNI, *meshAPI, *meshHeartbeat, *insecure, mm); err != nil {
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
	url := strings.TrimRight(api, "/") + "/api/portal/vpn/nodes"
	// 弱网下重试两次：一次失败就报「加载不出节点」体验太差
	resp, err := panelDo(ctx, func(c context.Context) (*http.Request, error) {
		return panelRequest(c, http.MethodGet, url, nil, token)
	}, 3)
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
	resp, err := panelHTTP.Do(req)
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

// meshState 保存数据面状态；RTT 取自 QUIC 长连接（真实链路时延），心跳只负责上报。
type meshState struct {
	mu       sync.Mutex
	conn     *quic.Conn
	endpoint string
	dataOK   bool
	lastErr  string
	rttMs    int
}

type meshReport struct {
	RTTms     int    `json:"rtt_ms"`
	Transport string `json:"transport"`
	State     string `json:"state"`
	Endpoint  string `json:"endpoint,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (s *meshState) setConn(conn *quic.Conn, endpoint string, handshake time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = conn
	s.endpoint = endpoint
	s.dataOK = conn != nil
	if conn != nil {
		s.lastErr = ""
	}
	if handshake > 0 {
		s.rttMs = int(handshake.Milliseconds())
	}
}

func (s *meshState) setDown(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = nil
	s.dataOK = false
	if err != nil {
		s.lastErr = err.Error()
	}
}

func (s *meshState) report() meshReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		if stats := s.conn.ConnectionStats(); stats.SmoothedRTT > 0 {
			s.rttMs = int(stats.SmoothedRTT.Milliseconds())
		}
	}
	state := "down"
	if s.dataOK {
		state = "up"
	}
	lastErr := s.lastErr
	if len(lastErr) > 180 {
		lastErr = lastErr[:180]
	}
	return meshReport{
		RTTms:     s.rttMs,
		Transport: "quic",
		State:     state,
		Endpoint:  s.endpoint,
		LastError: lastErr,
	}
}

func splitEndpoints(raw string) []string {
	out := make([]string, 0, 2)
	for _, item := range strings.Split(raw, ",") {
		if ep := strings.TrimSpace(item); ep != "" {
			out = append(out, ep)
		}
	}
	if len(out) == 0 {
		out = append(out, "vpncenter.yzfn.top:443")
	}
	return out
}

func runMesh(ctx context.Context, token, ip, cidr, tunName string, endpoints []string, sni, apiBase string, hbInterval time.Duration, insecure bool, mode TunMode) error {
	st := &meshState{}
	// 心跳先起：即使数据面暂时不通，面板也能看到"在线但 down"，而不是彻底失联。
	go meshHeartbeat(ctx, apiBase, token, hbInterval, st)

	backoff := 2 * time.Second
	for {
		if ctx.Err() != nil {
			return nil
		}
		started := time.Now()
		tun, err := meshDataPlane(ctx, endpoints, sni, token, ip, cidr, tunName, insecure, st, mode)
		if err == nil && tun != nil {
			// 数据面正常：等它自己断开
			for {
				select {
				case <-ctx.Done():
					tun.Close()
					return nil
				case <-time.After(time.Second):
					if e := tun.Err(); e != "" {
						err = errors.New(e)
					}
					if err != nil || ctx.Err() != nil {
						break
					}
				}
				if err != nil || ctx.Err() != nil {
					break
				}
			}
			tun.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		st.setDown(err)
		if time.Since(started) > 30*time.Second {
			backoff = 2 * time.Second
		}
		log.Printf("组网数据面断开：%v；%s 后重连", err, backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// meshDataPlane 维持一条到中心的长连接；失败按候选端点回退，不退出进程。
func meshDataPlane(ctx context.Context, endpoints []string, sni, token, localIP, cidr, tunName string, insecure bool, st *meshState, mode TunMode) (*Tunnel, error) {
	var lastErr error
	for _, ep := range endpoints {
		if ctx.Err() != nil {
			return nil, nil
		}
		start := time.Now()
		assigned, conn, err := authHTTP3(ctx, ep, sni, token, insecure)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", ep, err)
			log.Printf("组网鉴权失败（%s）：%v", ep, err)
			continue
		}
		st.setConn(conn, ep, time.Since(start))
		log.Printf("组网鉴权成功：节点分配 %s（本地 %s，端点 %s，握手 %s）",
			assigned, localIP, ep, time.Since(start).Round(time.Millisecond))

		if mode == "" {
			mode = TunModeMesh
		}
		pc, cleanupStream, serr := func() (*streamPacketConn, func(), error) {
			c, _, _, cl, e := openTunnelStream(ctx, ep, sni, token, insecure)
			return c, cl, e
		}()
		if serr != nil {
			_ = conn.CloseWithError(0, "stream-failed")
			lastErr = serr
			log.Printf("建立隧道流失败（%s）：%v", ep, serr)
			continue
		}
		tun, perr := StartTunnel(ctx, TunOptions{
			Spec: TunSpec{
				Name:     tunName,
				IP:       localIP,
				CIDR:     cidr,
				MTU:      1280,
				Mode:     mode,
				Endpoint: ep,
			},
			Conn: pc,
		})
		_ = cleanupStream
		if perr != nil {
			_ = conn.CloseWithError(0, "tun-failed")
			lastErr = perr
			log.Printf("建立隧道失败（%s）：%v", ep, perr)
			continue
		}
		return tun, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用端点")
	}
	return nil, lastErr
}

// meshHeartbeat 独立于数据面运行：上报存活、RTT 与数据面状态。
func meshHeartbeat(ctx context.Context, apiBase, token string, interval time.Duration, st *meshState) {
	if interval <= 0 {
		interval = 20 * time.Second
	}
	apiBase = strings.TrimRight(apiBase, "/")
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil, // 控制面直连，绝不走本地代理
			MaxIdleConns:          4,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
	send := func() {
		body, _ := json.Marshal(st.report())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+"/api/vpn/mesh/heartbeat", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("组网心跳失败：%v", err)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.Printf("组网心跳被拒：HTTP %d", resp.StatusCode)
		}
	}
	send()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

// authHTTP3 建立鉴权连接，失败会自动重试。
//
// 为什么要内建重试：UDP 443 的握手首个 Initial 包偶发丢失时，quic-go 直接返回
// 「timeout: no recent network activity」，用户看到的就是「连不上，要连好几次」。
// 这里做 3 次尝试 + 递增退避，把偶发丢包吃掉，而不是抛给用户去点重试。
func authHTTP3(ctx context.Context, endpoint, sni, token string, insecure bool) (string, *quic.Conn, error) {
	const attempts = 3
	var lastErr error
	for i := 1; i <= attempts; i++ {
		assigned, conn, err := authHTTP3Once(ctx, endpoint, sni, token, insecure)
		if err == nil {
			return assigned, conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		if i < attempts {
			// 400ms / 800ms 退避：给路径上的状态一次恢复机会
			wait := time.Duration(400<<uint(i-1)) * time.Millisecond
			log.Printf("HTTP/3 鉴权失败（第 %d/%d 次，%v 后重试）：%v", i, attempts, wait, err)
			select {
			case <-ctx.Done():
				return "", nil, ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
	}
	return "", nil, lastErr
}

func authHTTP3Once(ctx context.Context, endpoint, sni, token string, insecure bool) (string, *quic.Conn, error) {
	var conn *quic.Conn
	tlsConf := &tls.Config{
		ServerName:         sni,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h3"},
		InsecureSkipVerify: insecure,
	}
	if len(caPEM) > 0 {
		pool, perr := rootPoolWithPlatformCA()
		if perr != nil {
			return "", nil, perr
		}
		tlsConf.RootCAs = pool
		tlsConf.InsecureSkipVerify = false
	}
	tr := &http3.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig: &quic.Config{
			EnableDatagrams: true,
			KeepAlivePeriod: 10 * time.Second,
			MaxIdleTimeout:  60 * time.Second,
			// 握手超时从默认 5s 放宽到 15s：RTT 150~200ms 的跨境链路上，
			// 首个 Initial 包丢一次就要等 1s→2s→4s 的 PTO 退避，
			// 5s 很容易在第二次重传前就判死，表现为
			// 「timeout: no recent network activity」，用户要连好几次才成功。
			HandshakeIdleTimeout: 15 * time.Second,
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

// runTunSelftest 验证「创建网卡 + 配置地址 + 下发路由 + 清理」这条链路。
// 不需要真实 VPN 会话，适合安装后确认驱动与权限是否就绪。
func runTunSelftest(mode TunMode, keepSec int) error {
	if !runtimeSupportsTun() {
		return fmt.Errorf("当前平台不支持 TUN")
	}
	if keepSec <= 0 {
		keepSec = 5
	}
	spec := TunSpec{
		IP:   "10.99.0.250",
		CIDR: "10.99.0.0/16",
		MTU:  1420,
		Mode: mode,
	}
	log.Printf("自检开始：模式=%s", mode)

	dev, err := openTUN(spec.name())
	if err != nil {
		return err
	}
	res, err := applyTun(spec, dev)
	if err != nil {
		_ = dev.Close()
		return err
	}
	log.Printf("网卡已创建：%s", res.Summary())
	for _, r := range res.Routes {
		log.Printf("  路由：%s", r)
	}
	log.Printf("%d 秒后自动清理…", keepSec)
	time.Sleep(time.Duration(keepSec) * time.Second)

	cleanupTun(res)
	_ = dev.Close()
	log.Printf("自检完成：网卡与路由已清理")
	return nil
}

// runInstallMode 执行安装流程并输出结果。
func runInstallMode(dir string, autostart bool) {
	if !runtimeSupportsTun() {
		log.Fatalf("当前平台不支持安装流程")
	}
	res, err := RunInstall(InstallOptions{Dir: dir, Autostart: autostart})
	if err != nil {
		log.Printf("安装失败：%v", err)
		os.Exit(1)
	}
	printInstallResult(res)
	log.Printf("安装完成：网卡=%s 驱动=%s", res.Adapter, res.DriverPath)
}

// runUninstallMode 移除驱动注册与卸载信息。
func runUninstallMode() {
	if err := RunUninstall(); err != nil {
		log.Printf("卸载失败：%v", err)
		os.Exit(1)
	}
	log.Printf("已移除虚拟网卡与卸载信息")
}

// runAdapterStatus 检查网卡是否已注册（安装脚本用它做校验）。
func runAdapterStatus() {
	name, ok := adapterStatus()
	if ok {
		fmt.Printf("adapter=%s registered=yes\n", name)
		return
	}
	fmt.Printf("adapter=%s registered=no\n", name)
	os.Exit(2)
}

// runDiag 输出诊断报告到控制台，并另存一份到临时目录。
func runDiag() {
	report := diagReport(nil)
	fmt.Print(report)
	if path := logFilePath(); path != "" {
		fmt.Printf("\n日志文件: %s\n", path)
	}
	out := filepath.Join(os.TempDir(), "proxypilot-diag.txt")
	if err := os.WriteFile(out, []byte(report), 0o644); err == nil {
		fmt.Printf("报告已保存: %s\n", out)
	}
}
