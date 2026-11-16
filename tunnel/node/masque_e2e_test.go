package node

// MASQUE CONNECT-IP 端到端单测：本地起 http3.Server（带 TUN 替身），
// 客户端用 quic-go 的 ClientConn/RequestStream 走标准 Extended CONNECT，
// 验证 200 + capsule（ADDRESS_ASSIGN / ROUTE_ADVERTISEMENT）+ 双向 HTTP Datagram。
//
// 不依赖真实 TUN / 网络；与私有协议单测互补。

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

func masqueTestTLS(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "masque-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestMasqueConnectIPEndToEnd：标准 Extended CONNECT + capsule + 双向 IP Datagram。
func TestMasqueConnectIPEndToEnd(t *testing.T) {
	fake := newFakeTUN()
	fake.uplink = make(chan []byte, 16)
	srv, priv := masqueTestServer(t)
	srv.tun = fake
	go srv.tunLoop()
	defer fake.Close()

	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{masqueTestTLS(t)},
		NextProtos:   []string{"h3"},
		MinVersion:   tls.VersionTLS13,
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{
		Handler:         srv,
		EnableDatagrams: true,
		ConnContext: func(ctx context.Context, c *quic.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ServeListener(ln) }()
	defer func() {
		_ = server.Close()
		_ = ln.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 客户端：QUIC + HTTP/3 Extended CONNECT。
	conn, err := quic.DialAddr(ctx, ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		NextProtos:         []string{"h3"},
		MinVersion:         tls.VersionTLS13,
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "test done")
	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13},
		EnableDatagrams: true,
	}
	defer tr.Close()
	cc := tr.NewClientConn(conn)
	reqStream, err := cc.OpenRequestStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reqURL, _ := url.Parse("https://localhost/.well-known/masque/ip/*/*/")
	req := &http.Request{
		Method: http.MethodConnect,
		Proto:  "connect-ip",
		URL:    reqURL,
		Host:   "localhost",
		Header: http.Header{
			"Authorization":    {"Bearer " + masqueTestClaims(t, priv, "10.88.3.10")},
			"Capsule-Protocol": {"?1"},
		},
	}
	if err := reqStream.SendRequestHeader(req); err != nil {
		t.Fatal(err)
	}
	resp, err := reqStream.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if resp.Header.Get("Capsule-Protocol") != "?1" {
		t.Fatal("缺少 Capsule-Protocol 响应头")
	}
	if got := resp.Header.Get("X-VPN-Assigned-IPv4"); got != "10.88.3.10" {
		t.Fatalf("分配地址头 = %q", got)
	}

	// 读服务端主动下发的两个 capsule。
	br := bufio.NewReader(reqStream)
	ct, value, err := readCapsule(br)
	if err != nil {
		t.Fatalf("读 ADDRESS_ASSIGN: %v", err)
	}
	if ct != capsuleAddressAssign {
		t.Fatalf("first capsule type=%d，期望 ADDRESS_ASSIGN", ct)
	}
	addrs, err := parseAddressList(value)
	if err != nil || len(addrs) != 1 || !addrs[0].IP.Equal(net.ParseIP("10.88.3.10")) || addrs[0].PrefixLen != 32 {
		t.Fatalf("ADDRESS_ASSIGN = %+v err=%v", addrs, err)
	}
	ct, value, err = readCapsule(br)
	if err != nil || ct != capsuleRouteAdvertisement {
		t.Fatalf("second capsule type=%d err=%v", ct, err)
	}
	routes, err := parseRouteAdvertisement(value)
	if err != nil || len(routes) != 1 || !routes[0].Start.Equal(net.IPv4zero) || !routes[0].End.Equal(net.IPv4bcast) {
		t.Fatalf("ROUTE_ADVERTISEMENT = %+v err=%v", routes, err)
	}
	// 后台继续读 capsule，避免 ADDRESS_REQUEST 响应堆积（本测试后面会发一个）。
	go func() {
		b := bufio.NewReader(reqStream)
		for {
			if _, _, err := readCapsule(b); err != nil {
				return
			}
		}
	}()

	// 上行：客户端发一个 IPv4 包（src=10.88.3.10，dst=1.1.1.1，ICMP）。
	up := makeIPv4(1, net.ParseIP("10.88.3.10").To4(), net.ParseIP("1.1.1.1").To4(), []byte{8, 0, 0, 0, 0, 1, 0, 0})
	if err := reqStream.SendDatagram(masqueDatagramPayload(up)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fake.uplink:
		if len(got) < 20 || binary.BigEndian.Uint16(got[2:4]) != uint16(len(got)) {
			t.Fatalf("TUN 收到异常报文：% x", got)
		}
		if !net.IP(got[12:16]).Equal(net.ParseIP("10.88.3.10")) {
			t.Fatalf("TUN 收到报文源地址 = %v", net.IP(got[12:16]))
		}
	case <-ctx.Done():
		t.Fatal("等待上行写入 TUN 超时")
	}

	// 下行：TUN 读到一个包 → 节点应通过该会话的 HTTP Datagram 发回客户端。
	// 先等服务端会话注册完成（200 已返回，理论上已注册；这里做一次显式校验）。
	if srv.lookup(net.ParseIP("10.88.3.10").To4()) == nil {
		t.Fatal("MASQUE 会话未注册到 sessionSet")
	}
	down := makeIPv4(6, net.ParseIP("1.1.1.1").To4(), net.ParseIP("10.88.3.10").To4(), nil)
	fake.ch <- down
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	payload, err := reqStream.ReceiveDatagram(rctx)
	if err != nil {
		t.Fatalf("接收下行 datagram: %v", err)
	}
	ctxID, data, err := parseMasqueDatagram(payload)
	if err != nil || ctxID != 0 {
		t.Fatalf("下行 datagram 解析失败：ctx=%d err=%v", ctxID, err)
	}
	if len(data) != len(down) || !net.IP(data[16:20]).Equal(net.ParseIP("10.88.3.10")) {
		t.Fatalf("下行报文不匹配：% x", data)
	}

	// ADDRESS_REQUEST：requestID=7 请求 0.0.0.0/32，服务端应回匹配 requestID 的分配。
	reqPayload, err := marshalAddressList([]masqueAddress{{RequestID: 7, IP: net.IPv4zero.To4(), PrefixLen: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCapsule(reqStream, capsuleAddressRequest, reqPayload); err != nil {
		t.Fatal(err)
	}
	// 后台读 goroutine 会消费掉；这里直接用新连接读不合适，故只验证服务端不报错：
	// 通过查询 session 仍存在间接确认。
	time.Sleep(100 * time.Millisecond)
	if srv.lookup(net.ParseIP("10.88.3.10").To4()) == nil {
		t.Fatal("ADDRESS_REQUEST 后会话不应消失")
	}
}

// makeIPv4 造一个最小 IPv4 报文（头 20 字节 + 可选载荷）。
func makeIPv4(proto byte, src, dst net.IP, payload []byte) []byte {
	total := 20 + len(payload)
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64
	pkt[9] = proto
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	copy(pkt[20:], payload)
	return pkt
}

// TestMasqueUplinkChecksumFixEndToEnd：task-34 回归。
// 手工探针/校验和卸载可能产生 IPv4 头校验和为 0 / 错误的包；服务端必须在写 TUN
// 前修正，否则内核 ip_rcv 静默丢弃 → 会话 tx 增长但 rx=0（现象：DNS 永不回包）。
// 本测试覆盖：坏 IPv4 校验和 → 修正后写入 TUN → 内核「回包」→ 下行 SendDatagram → 客户端收到。
func TestMasqueUplinkChecksumFixEndToEnd(t *testing.T) {
	fake := newFakeTUN()
	fake.uplink = make(chan []byte, 16)
	srv, priv := masqueTestServer(t)
	srv.tun = fake
	go srv.tunLoop()
	defer fake.Close()

	ln, err := quic.ListenAddr("127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{masqueTestTLS(t)},
		NextProtos:   []string{"h3"},
		MinVersion:   tls.VersionTLS13,
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	server := &http3.Server{
		Handler:         srv,
		EnableDatagrams: true,
		ConnContext: func(ctx context.Context, c *quic.Conn) context.Context {
			return context.WithValue(ctx, connKey{}, c)
		},
	}
	go func() { _ = server.ServeListener(ln) }()
	defer func() { _ = server.Close(); _ = ln.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, ln.Addr().String(), &tls.Config{
		InsecureSkipVerify: true, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13,
	}, &quic.Config{EnableDatagrams: true})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseWithError(0, "test done")
	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h3"}, MinVersion: tls.VersionTLS13},
		EnableDatagrams: true,
	}
	defer tr.Close()
	reqStream, err := tr.NewClientConn(conn).OpenRequestStream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reqURL, _ := url.Parse("https://localhost/.well-known/masque/ip/*/*/")
	req := &http.Request{
		Method: http.MethodConnect, Proto: "connect-ip", URL: reqURL, Host: "localhost",
		Header: http.Header{
			"Authorization":    {"Bearer " + masqueTestClaims(t, priv, "10.88.3.10")},
			"Capsule-Protocol": {"?1"},
		},
	}
	if err := reqStream.SendRequestHeader(req); err != nil {
		t.Fatal(err)
	}
	resp, err := reqStream.ReadResponse()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT-IP 失败：%v status=%v", err, resp)
	}
	// 读掉服务端主动下发的 ADDRESS_ASSIGN / ROUTE_ADVERTISEMENT（确保会话已注册）。
	br := bufio.NewReader(reqStream)
	for i := 0; i < 2; i++ {
		if _, _, err := readCapsule(br); err != nil {
			t.Fatalf("读取初始化 capsule 失败：%v", err)
		}
	}

	ip := net.ParseIP("10.88.3.10").To4()
	set := srv.lookup(ip)
	if set == nil || set.masque == nil {
		t.Fatal("MASQUE 会话未注册")
	}
	sess := set.masque
	src := [4]byte{10, 88, 3, 10}
	dstGW := [4]byte{10, 88, 3, 1}
	dns := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 0, 0, 1, 0, 1}

	// 1) IPv4 头校验和 0（模拟校验和卸载）→ 服务端应修正后写 TUN。
	badL3 := testUDPPacket(src, dstGW, 40010, 53, dns, false)
	binary.BigEndian.PutUint16(badL3[10:12], 0)
	if err := reqStream.SendDatagram(masqueDatagramPayload(badL3)); err != nil {
		t.Fatal(err)
	}
	var written []byte
	select {
	case written = <-fake.uplink:
	case <-time.After(3 * time.Second):
		t.Fatal("上行未写入 TUN（超时）")
	}
	if !ipv4HeaderChecksumOK(written[:20]) {
		t.Fatalf("写入 TUN 的 IPv4 校验和未修正：% x", written[:20])
	}
	waitFor(t, 2*time.Second, func() bool { return sess.masqueFixedL3 == 1 && sess.tx == 1 })

	// 2) UDP 校验和错误 → 服务端应修正 L4 后写 TUN。
	badL4 := testUDPPacket(src, dstGW, 40011, 53, dns, false)
	binary.BigEndian.PutUint16(badL4[26:28], 0xbeef)
	if err := reqStream.SendDatagram(masqueDatagramPayload(badL4)); err != nil {
		t.Fatal(err)
	}
	select {
	case written = <-fake.uplink:
	case <-time.After(3 * time.Second):
		t.Fatal("上行未写入 TUN（超时）")
	}
	if !l4ChecksumOK(written, 20, 17) {
		t.Fatal("写入 TUN 的 UDP 校验和未修正")
	}
	waitFor(t, 2*time.Second, func() bool { return sess.masqueFixedL4 == 1 && sess.tx == 2 })

	// 3) 模拟内核回包（交换五元组并重算校验和）→ 走 tunLoop/pick/kindMasque/send。
	reply := make([]byte, len(written))
	copy(reply, written)
	copy(reply[12:16], written[16:20])
	copy(reply[16:20], written[12:16])
	// 交换 UDP 端口并重算 L4/IPv4 校验和。
	reply[20], reply[22] = written[22], written[20]
	reply[21], reply[23] = written[23], written[21]
	binary.BigEndian.PutUint16(reply[10:12], 0)
	binary.BigEndian.PutUint16(reply[10:12], ipv4HeaderChecksum(reply[:20]))
	binary.BigEndian.PutUint16(reply[26:28], 0)
	setL4Checksum(reply, 20, 17, 6)
	fake.ch <- reply
	rctx, rcancel := context.WithTimeout(ctx, 5*time.Second)
	defer rcancel()
	payload, err := reqStream.ReceiveDatagram(rctx)
	if err != nil {
		t.Fatalf("接收下行 datagram 失败：%v", err)
	}
	ctxID, data, err := parseMasqueDatagram(payload)
	if err != nil || ctxID != 0 {
		t.Fatalf("下行 datagram 解析失败：ctx=%d err=%v", ctxID, err)
	}
	if len(data) < 28 || data[9] != 17 || binary.BigEndian.Uint16(data[20:22]) != 53 {
		t.Fatalf("下行报文不是 DNS 响应：% x", data)
	}
	if !l4ChecksumOK(data, 20, 17) {
		t.Fatal("下行 DNS 响应校验和异常")
	}
	if sess.rx != 1 {
		t.Fatalf("会话 rx=%d，期望 1", sess.rx)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}
