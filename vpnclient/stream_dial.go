package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// openTunnelStream 建立一条可靠的 HTTP/3 双向流作为隧道数据面。
//
// 返回的 pw 写「2 字节长度 + IP 报文」，响应体读同样格式的下行报文。
func openTunnelStream(ctx context.Context, endpoint, sni, token string, insecure bool) (*streamPacketConn, func(), error) {
	var conn *quic.Conn
	tlsConf := &tls.Config{
		ServerName:         sni,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h3"},
		InsecureSkipVerify: insecure,
	}
	if len(caPEM) > 0 {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, nil, fmt.Errorf("CA 证书无效")
		}
		tlsConf.RootCAs = pool
		tlsConf.InsecureSkipVerify = false
	}

	// 流控窗口按 BDP 放大：100 Mbps × 400ms ≈ 5 MB。
	// 默认 512KB 在 380ms 跨境链路上会把单流卡在 ~10 Mbps。
	quicConf := &quic.Config{
		KeepAlivePeriod:                10 * time.Second,
		MaxIdleTimeout:                 120 * time.Second,
		InitialStreamReceiveWindow:     4 << 20,
		MaxStreamReceiveWindow:         16 << 20,
		InitialConnectionReceiveWindow: 8 << 20,
		MaxConnectionReceiveWindow:     64 << 20,
	}
	tr := &http3.Transport{
		TLSClientConfig: tlsConf,
		QUICConfig:      quicConf,
		Dial: func(ctx context.Context, addr string, t *tls.Config, c *quic.Config) (*quic.Conn, error) {
			qc, err := quic.DialAddr(ctx, addr, t, c)
			if err == nil {
				conn = qc
			}
			return qc, err
		},
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+endpoint+"/tunnel", pr)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := tr.RoundTrip(req)
	if err != nil {
		return nil, nil, err
	}
	if got := resp.Header.Get("X-VPN-Auth"); got != "Success" {
		_ = resp.Body.Close()
		return nil, nil, fmt.Errorf("X-VPN-Auth=%q status=%d", got, resp.StatusCode)
	}

	cleanup := func() {
		_ = pw.Close()
		_ = resp.Body.Close()
		if conn != nil {
			_ = conn.CloseWithError(0, "tunnel closed")
		}
		tr.Close()
	}
	pc := newStreamPacketConn(pw, resp.Body)
	return pc, cleanup, nil
}