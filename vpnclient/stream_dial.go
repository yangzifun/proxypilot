package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// openTunnelStream 建立一条可靠的 HTTP/3 双向流作为隧道数据面。
//
// 返回的 pw 写「2 字节长度 + IP 报文」，响应体读同样格式的下行报文。
// openTunnelStream 建立隧道数据面。
//
// 返回值里额外带上底层 QUIC 连接：DATAGRAM 必须走**同一条连接**，
// 否则它会挂在那条空闲的鉴权连接上（cwnd 极小、甚至连接已被回收），
// 表现为「客户端发出 343 个、节点只收到 129 个，双方都没有错误计数」。
func openTunnelStream(ctx context.Context, endpoint, sni, token string, insecure bool) (*streamPacketConn, *quic.Conn, bool, func(), error) {
	return openTunnelStreamLane(ctx, endpoint, sni, token, insecure, 0)
}

// openTunnelStreamLane 打开一条指定 lane 的隧道流。
//
// lane=0 是主可靠流；lane>0 是额外的一条独立 QUIC 流，
// 与主流通用同一条 QUIC 连接，但拥有独立的流控与重传队列。
func openTunnelStreamLane(ctx context.Context, endpoint, sni, token string, insecure bool, lane int) (*streamPacketConn, *quic.Conn, bool, func(), error) {
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
			return nil, nil, false, nil, perr
		}
		tlsConf.RootCAs = pool
		tlsConf.InsecureSkipVerify = false
	}

	// 流控窗口按 BDP 放大：100 Mbps × 400ms ≈ 5 MB。
	// 默认 512KB 在 380ms 跨境链路上会把单流卡在 ~10 Mbps。
	quicConf := &quic.Config{
		// 关键：隧道流这条连接必须自己开启 DATAGRAM。
		// 之前只有鉴权连接开了，单连接改造后 DATAGRAM 改走这条连接，
		// 但这里没开 —— ReceiveDatagram 立刻返回 "datagram support disabled"，
		// datagramLoop 启动即退出，UDP 下行永远是 0。
		EnableDatagrams: true,
		KeepAlivePeriod: 10 * time.Second,
		MaxIdleTimeout:  120 * time.Second,
		// 与鉴权连接同理：跨境链路上握手丢一次包就要 PTO 退避，
		// 默认 5s 太紧，放宽到 15s 减少「首次连不上」。
		HandshakeIdleTimeout:           15 * time.Second,
		InitialStreamReceiveWindow:     4 << 20,
		MaxStreamReceiveWindow:         16 << 20,
		InitialConnectionReceiveWindow: 8 << 20,
		MaxConnectionReceiveWindow:     64 << 20,
	}
	// 自建 UDP socket 并放大收发缓冲。
	//
	// BDP = 带宽 × RTT。100 Mbps × 145 ms ≈ 1.8 MB；
	// 用默认 64KB 缓冲时，单流吞吐会被压在十几 Mbps 量级。
	// 交给 quic.Transport 接管后还能启用批量收发（Linux GSO / recvmmsg）。
	udpConn, uerr := net.ListenUDP("udp4", nil)
	if uerr != nil {
		return nil, nil, false, nil, fmt.Errorf("创建 UDP socket 失败：%w", uerr)
	}
	_ = udpConn.SetReadBuffer(socketBufferBytes)
	_ = udpConn.SetWriteBuffer(socketBufferBytes)
	qtr := &quic.Transport{Conn: udpConn}

	tr := &http3.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig:      quicConf,
		EnableDatagrams: true,
		Dial: func(ctx context.Context, addr string, t *tls.Config, c *quic.Config) (*quic.Conn, error) {
			ua, rerr := net.ResolveUDPAddr("udp4", addr)
			if rerr != nil {
				return nil, rerr
			}
			qc, err := qtr.Dial(ctx, ua, t, c)
			if err == nil {
				conn = qc
			}
			return qc, err
		},
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+endpoint+"/tunnel", pr)
	if err != nil {
		return nil, nil, false, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	// 能力协商（参考 MASQUE / RFC 9297）：
	// 声明本端支持 capsule 与 HTTP Datagram，便于节点回同样的能力位。
	// 之前两端各开各的，任一侧缺失都表现为「静默丢包」。
	req.Header.Set("Capsule-Protocol", "?1")
	req.Header.Set("Connect-Protocol", "connect-ip")
	req.Header.Set("H3-Datagram", "?1")
	if lane > 0 {
		req.Header.Set("X-VPN-Lane", fmt.Sprintf("%d", lane))
		// 附加流不需要重跑鉴权，但节点仍会校验 JWT 并复用同一设备 IP。
	}

	resp, err := tr.RoundTrip(req)
	if err != nil {
		return nil, nil, false, nil, err
	}
	if got := resp.Header.Get("X-VPN-Auth"); got != "Success" {
		_ = resp.Body.Close()
		return nil, nil, false, nil, fmt.Errorf("X-VPN-Auth=%q status=%d", got, resp.StatusCode)
	}
	// 节点是否确认支持 HTTP Datagram。只有它回了 H3-Datagram，客户端才启用双通道；
	// 否则即使本地 EnableDatagrams=true，对端也可能根本不发 datagram。
	serverDatagram := resp.Header.Get("H3-Datagram") != ""
	log.Printf("数据面：隧道流已建立（节点 Datagram 能力=%v）", serverDatagram)

	cleanup := func() {
		_ = pw.Close()
		_ = resp.Body.Close()
		if conn != nil {
			_ = conn.CloseWithError(0, "tunnel closed")
		}
		tr.Close()
		if qtr != nil {
			_ = qtr.Close()
		}
		if udpConn != nil {
			_ = udpConn.Close()
		}
	}
	pc := newStreamPacketConn(pw, resp.Body)
	return pc, conn, serverDatagram, cleanup, nil
}
