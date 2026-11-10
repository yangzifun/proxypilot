package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

// 这是数据面的核心回归测试：验证「请求体持续上传」与「响应体持续下行」
// 在同一个 HTTP/3 流上能真正并行工作。
//
// 线上症状：客户端显示 ↑ 有流量、↓ 恒为 0；节点日志
//   流隧道会话建立 … / 流隧道会话结束 rx=0 tx=0
// 说明上行帧根本没到达对端 —— 即「请求体没有真的被发出去」。
// 这个测试不需要 TUN、不需要网络，本机就能判定是协议用法问题还是环境问题。
func TestStreamDuplexOverHTTP3(t *testing.T) {
	// ---- 自签证书（仅测试）----
	certPEM, keyPEM, err := selfSignedForTest()
	if err != nil {
		t.Fatal(err)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}

	// ---- 回显服务端：读一帧 → 原样写回一帧 ----
	got := make(chan []byte, 8)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tunnel", func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("X-VPN-Auth", "Success")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}
		var hdr [2]byte
		for {
			if _, err := io.ReadFull(r.Body, hdr[:]); err != nil {
				return
			}
			n := int(binary.BigEndian.Uint16(hdr[:]))
			pkt := make([]byte, n)
			if _, err := io.ReadFull(r.Body, pkt); err != nil {
				return
			}
			got <- pkt
			// 回显：同格式写回
			out := make([]byte, 2+n)
			binary.BigEndian.PutUint16(out[:2], uint16(n))
			copy(out[2:], pkt)
			if _, err := w.Write(out); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	})

	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		NextProtos:   []string{"h3"},
	}, &quic.Config{})
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	defer ln.Close()

	srv := &http3.Server{Handler: mux, QUICConfig: &quic.Config{}}
	go func() { _ = srv.ServeListener(ln) }()
	defer srv.Close()

	addr := ln.Addr().String()

	// ---- 客户端：等价于 openTunnelStream 的用法 ----
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+"/tunnel", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer test")

	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{
			ServerName:         "localhost",
			MinVersion:         tls.VersionTLS13,
			NextProtos:         []string{"h3"},
			InsecureSkipVerify: true,
		},
	}
	defer tr.Close()

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip 失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("X-VPN-Auth") != "Success" {
		t.Fatalf("鉴权头不符：%q", resp.Header.Get("X-VPN-Auth"))
	}

	pc := newStreamPacketConn(pw, resp.Body)
	defer pc.Close()

	// 上行：发一个包，必须真的到达服务端
	pkt := []byte{0x45, 0x00, 0x00, 0x1c, 0xde, 0xad}
	if err := pc.SendPacket(pkt); err != nil {
		t.Fatalf("SendPacket 失败：%v", err)
	}

	select {
	case s := <-got:
		if string(s) != string(pkt) {
			t.Fatalf("服务端收到的包不一致：want=%x got=%x (len %d)", pkt, s, len(s))
		}
	case <-time.After(8 * time.Second):
		t.Fatal("服务端 8 秒内没收到任何帧 —— 请求体没有被真正发送（这就是 ↑有流量 ↓恒为0 的根因）")
	}

	// 下行：必须能读回
	doneCh := make(chan []byte, 1)
	go func() {
		b, err := pc.RecvPacket()
		if err != nil {
			doneCh <- nil
			return
		}
		doneCh <- b
	}()
	select {
	case b := <-doneCh:
		if b == nil {
			t.Fatal("下行读取失败")
		}
		if string(b) != string(pkt) {
			t.Fatalf("下行包不一致：%x", b)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("8 秒内没读到下行帧 —— 响应体没有及时交付（这就是 ↓ 恒为 0 的根因）")
	}
}

// selfSignedForTest 生成仅用于测试的自签证书。
func selfSignedForTest() ([]byte, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:         true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if certPEM == nil || keyPEM == nil {
		return nil, nil, fmt.Errorf("PEM 编码失败")
	}
	return certPEM, keyPEM, nil
}