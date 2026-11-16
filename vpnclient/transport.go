package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/yzfn/proxypilot/vpnclient/internal/masque"
)

// 传输协议选择（客户端设置项 protocol）：
//   - auto：先 PilotLink（私有协议），失败回退 MASQUE（默认）
//   - private：只用 PilotLink（自研 HTTP/3 隧道）
//   - masque：只用标准 MASQUE CONNECT-IP
const (
	protocolAuto    = "auto"
	protocolPrivate = "private"
	protocolMasque  = "masque"
)

// normalizeProtocol 接受用户/面板可能传来的多种写法。
func normalizeProtocol(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case protocolMasque, "mq", "connect-ip", "connectip":
		return protocolMasque
	case protocolPrivate, "tunnel", "pp", "pilotlink", "pilot-link":
		return protocolPrivate
	default:
		return protocolAuto
	}
}

// protocolLabel 用于日志/UI 展示。
func protocolLabel(proto string) string {
	switch normalizeProtocol(proto) {
	case protocolMasque:
		return "MASQUE"
	case protocolPrivate:
		return "PilotLink"
	default:
		return "自动"
	}
}

// transportOrder 返回本次连接尝试顺序；显式选择不想要的协议不会回退。
func transportOrder(pref string, n node) []string {
	switch normalizeProtocol(pref) {
	case protocolPrivate:
		return []string{protocolPrivate}
	case protocolMasque:
		return []string{protocolMasque}
	default:
		// auto：private → masque；节点明确声明不支持 masque 时只试 private。
		if len(n.Protocols) > 0 && !nodeSupportsMasque(n) {
			return []string{protocolPrivate}
		}
		return []string{protocolPrivate, protocolMasque}
	}
}

// nodeSupportsMasque 判断节点是否启用 MASQUE：
// 旧面板不返回 protocols（空）时按“未知”处理，允许尝试（失败会回退/报错）。
func nodeSupportsMasque(n node) bool {
	if len(n.Protocols) == 0 {
		return true
	}
	for _, p := range n.Protocols {
		if normalizeProtocol(p) == protocolMasque {
			return true
		}
	}
	return false
}

// masquePath 返回 CONNECT-IP 请求路径：优先面板下发，其次标准默认值。
func masquePath(prof *profile) string {
	if prof != nil {
		if p := strings.TrimSpace(prof.MasquePath); p != "" {
			return p
		}
	}
	return masque.DefaultPath
}

// dataPlane 是建立好的数据面（PilotLink 多流/双通道 或 MASQUE）。
type dataPlane struct {
	conn     packetConn
	quicConn *quic.Conn
	streamPC *streamPacketConn // PilotLink 主可靠流（用于上行丢包统计），MASQUE 为 nil
	dual     *dualPacketConn
	protocol string
	fallback string
	cleanup  func()
}

// dialDataPlane 按 order 依次尝试传输；返回最后成功的一个。
// auto 模式下失败会清理半成品并继续下一个协议，错误信息汇总所有尝试原因。
func (s *uiServer) dialDataPlane(ctx context.Context, attempt int64, cfg uiSettings, prof *profile, pick node, sni string, skipVerify bool, order []string) (*dataPlane, error) {
	var failures []string
	for i, proto := range order {
		var (
			dp  *dataPlane
			err error
		)
		switch proto {
		case protocolMasque:
			dp, err = s.dialMasqueTransport(ctx, prof, pick, sni, skipVerify)
		default:
			dp, err = s.dialPrivateTransport(ctx, attempt, prof, pick, sni, skipVerify)
		}
		if err == nil {
			if i > 0 {
				dp.fallback = fmt.Sprintf("（自动回退：%s 失败后改用 %s）", protocolLabel(order[0]), protocolLabel(dp.protocol))
			}
			return dp, nil
		}
		failures = append(failures, fmt.Sprintf("%s: %v", protocolLabel(proto), err))
		log.Printf("数据面：%s 建立失败：%v", protocolLabel(proto), err)
	}
	return nil, fmt.Errorf("所有传输方式均失败：%s", strings.Join(failures, "；"))
}

// dialPrivateTransport 建立现有 PilotLink（私有协议）数据面（自研 HTTP/3 可靠流 + 可选 DATAGRAM 双通道）。
func (s *uiServer) dialPrivateTransport(ctx context.Context, attempt int64, prof *profile, pick node, sni string, skipVerify bool) (*dataPlane, error) {
	assigned, authConn, err := authHTTP3(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
	if err != nil {
		return nil, fmt.Errorf("HTTP/3 鉴权失败: %w", err)
	}
	log.Printf("PilotLink（私有协议）：HTTP/3 鉴权成功（节点分配 %s）", assigned)

	pc, streamConn, nodeHasDatagram, cleanupStream, serr := openTunnelStream(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
	lanes := make([]packetConn, 0, multiLaneCount)
	laneCleanups := make([]func(), 0, multiLaneCount)
	if serr == nil {
		for li := 1; li <= multiLaneCount; li++ {
			lpc, _, _, lclean, lerr := openTunnelStreamLane(ctx, pick.Endpoint, sni, prof.Token, skipVerify, li)
			if lerr != nil {
				log.Printf("PilotLink：lane %d 建立失败（%v），本次只用 %d 条可靠流", li, lerr, len(lanes)+1)
				break
			}
			laneCleanups = append(laneCleanups, lclean)
			lanes = append(lanes, lpc)
		}
	}
	cleanup := func() {
		for _, c := range laneCleanups {
			c()
		}
		if cleanupStream != nil {
			cleanupStream()
		}
		_ = authConn.CloseWithError(0, "closed")
	}
	if serr != nil {
		cleanup()
		return nil, fmt.Errorf("建立隧道流失败: %w", serr)
	}

	fecGroup := 0
	if prof.FECMode == "xor" && prof.FECGroup > 1 {
		fecGroup = prof.FECGroup
	}
	var pconn packetConn = pc
	var dual *dualPacketConn
	switch {
	case !prof.DatagramEnabled:
		log.Printf("PilotLink：面板已关闭 DATAGRAM，全部走可靠流")
	case !nodeHasDatagram || !supportsDatagrams(streamConn):
		log.Printf("PilotLink：对端未协商 DATAGRAM，全部走可靠流")
	default:
		dual = newDualPacketConnLanes(pc, lanes, quicDatagramPeer{conn: streamConn}, maxDatagramPayload, fecGroup)
		pconn = dual
		s.setFor(attempt, func(st *sessionState) { st.dualConn = dual })
		log.Printf("PilotLink：双通道已启用（UDP→DATAGRAM，TCP/ICMP→可靠流，FEC=%d）", fecGroup)
	}
	cleanupConn := func() {
		_ = pconn.Close()
		cleanup()
	}
	return &dataPlane{
		conn:     pconn,
		quicConn: streamConn,
		streamPC: pc,
		dual:     dual,
		protocol: protocolPrivate,
		cleanup:  cleanupConn,
	}, nil
}

// dialMasqueTransport 建立标准 MASQUE CONNECT-IP 数据面。
func (s *uiServer) dialMasqueTransport(ctx context.Context, prof *profile, pick node, sni string, skipVerify bool) (*dataPlane, error) {
	if !nodeSupportsMasque(pick) {
		return nil, fmt.Errorf("节点未启用 MASQUE（protocols=%v）", pick.Protocols)
	}
	path := masquePath(prof)
	endpoint := dialAddrWithCache(pick.Endpoint)
	tlsConf := &tls.Config{
		ServerName:         sni,
		NextProtos:         []string{"h3"},
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: skipVerify,
	}
	if !skipVerify {
		if pool, err := rootPoolWithPlatformCA(); err == nil && pool != nil {
			tlsConf.RootCAs = pool
		}
	}
	start := time.Now()
	mconn, err := masque.Dial(ctx, masque.DialConfig{
		Endpoint: endpoint,
		SNI:      sni,
		Token:    prof.Token,
		Path:     path,
		TLS:      tlsConf,
		Timeout:  15 * time.Second,
		Logger:   log.Default(),
	})
	if err != nil {
		return nil, fmt.Errorf("MASQUE 连接失败: %w", err)
	}
	log.Printf("MASQUE：CONNECT-IP 已建立（耗时 %s，endpoint=%s path=%s）", time.Since(start).Round(time.Millisecond), endpoint, path)

	// 服务端（tunnel/node）在响应头里下发实际分配 IP 与 MTU；以服务端为准，
	// 同时规避 QUIC DATAGRAM 的载荷上限（内层 MTU 不能超过服务端宣告值）。
	if v := mconn.AssignedIPv4Header(); v != "" {
		if prof.IP4 != "" && prof.IP4 != v {
			log.Printf("MASQUE：服务端分配 IP=%s 与面板 IP=%s 不一致，采用服务端分配", v, prof.IP4)
		}
		prof.IP4 = v
	}
	if mtu := mconn.MTU(); mtu > 0 && (prof.MTU == 0 || mtu < prof.MTU) {
		log.Printf("MASQUE：按服务端宣告调整隧道 MTU %d → %d", prof.MTU, mtu)
		prof.MTU = mtu
	}

	waitCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	assignedErr := mconn.WaitAssigned(waitCtx)
	cancel()
	if assignedErr != nil {
		log.Printf("MASQUE：等待 ADDRESS_ASSIGN 超时（%v），沿用面板分配 IP=%s", assignedErr, prof.IP4)
	} else {
		for _, a := range mconn.Assigned() {
			log.Printf("MASQUE：ADDRESS_ASSIGN %s/%d", a.IP, a.PrefixLen)
			if ip4 := a.IP.To4(); ip4 != nil && prof.IP4 != "" {
				if parsed := net.ParseIP(prof.IP4).To4(); parsed != nil && !parsed.Equal(ip4) {
					log.Printf("MASQUE：服务端分配 %s 与面板下发 IP %s 不一致，TUN 仍使用面板地址（请核对服务端实现）", a.IP, prof.IP4)
				}
			}
		}
	}
	if routes := mconn.Routes(); len(routes) > 0 {
		for _, r := range routes {
			log.Printf("MASQUE：ROUTE_ADVERTISEMENT %s - %s proto=%d", r.IP, r.EndIP, r.Protocol)
		}
	}
	return &dataPlane{
		conn:     mconn,
		quicConn: mconn.QUICConn(),
		protocol: protocolMasque,
		cleanup:  func() { _ = mconn.Close() },
	}, nil
}
