package masque

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/quic-go/quic-go/http3"
)

func TestCapsuleRoundTrip(t *testing.T) {
	addrs := []AssignedAddress{
		{RequestID: 1, IP: net.ParseIP("10.88.7.2").To4(), PrefixLen: 24},
		{RequestID: 2, IP: net.ParseIP("2001:db8::2"), PrefixLen: 64},
	}
	payload, err := EncodeAddressAssign(addrs)
	if err != nil {
		t.Fatalf("编码 ADDRESS_ASSIGN 失败：%v", err)
	}
	got, err := DecodeAddressAssign(payload)
	if err != nil {
		t.Fatalf("解码 ADDRESS_ASSIGN 失败：%v", err)
	}
	if len(got) != 2 || !got[0].IP.Equal(addrs[0].IP) || got[0].PrefixLen != 24 || !got[1].IP.Equal(addrs[1].IP) {
		t.Fatalf("ADDRESS_ASSIGN 往返不一致：%+v", got)
	}

	reqs := []RequestedAddress{{RequestID: 7, IP: net.ParseIP("10.88.7.2").To4(), PrefixLen: 32}}
	reqPayload, err := EncodeAddressRequest(reqs)
	if err != nil {
		t.Fatalf("编码 ADDRESS_REQUEST 失败：%v", err)
	}
	decoded, err := DecodeAddressRequest(reqPayload)
	if err != nil || len(decoded) != 1 || !decoded[0].IP.Equal(reqs[0].IP) || decoded[0].RequestID != 7 {
		t.Fatalf("ADDRESS_REQUEST 往返不一致：%+v err=%v", decoded, err)
	}

	// ROUTE_ADVERTISEMENT: v4 10.0.0.0 - 10.255.255.255 / proto 0
	var routePayload []byte
	routePayload = append(routePayload, 4)
	routePayload = append(routePayload, net.ParseIP("10.0.0.0").To4()...)
	routePayload = append(routePayload, net.ParseIP("10.255.255.255").To4()...)
	routePayload = append(routePayload, 0)
	routes, err := DecodeRouteAdvertisement(routePayload)
	if err != nil || len(routes) != 1 || routes[0].PrefixLen != 8 || routes[0].Protocol != 0 {
		t.Fatalf("ROUTE_ADVERTISEMENT 解析不一致：%+v err=%v", routes, err)
	}

	// DATAGRAM payload（Context ID=0 + IP 包）
	pkt := makeIPv4UDP([]byte{1, 2, 3, 4})
	dp := AppendDatagramPayload(nil, pkt)
	ctxID, data, err := ParseDatagramPayload(dp)
	if err != nil || ctxID != 0 || !bytes.Equal(data, pkt) {
		t.Fatalf("HTTP Datagram payload 往返不一致：ctx=%d err=%v", ctxID, err)
	}
}

func TestCapsuleFramingPartial(t *testing.T) {
	full := AppendCapsule(nil, CapsuleAddressAssign, []byte{1, 4, 10, 0, 0, 2, 24})
	var buf bytes.Buffer
	buf.Write(full)
	capsule, err := ReadCapsule(&buf)
	if err != nil {
		t.Fatalf("ReadCapsule 失败：%v", err)
	}
	if capsule.Type != CapsuleAddressAssign || len(capsule.Value) != 7 {
		t.Fatalf("capsule 解析结果异常：%+v", capsule)
	}
	// 不完整数据：只给一半，应返回错误。
	half := full[:len(full)-2]
	if _, err := ReadCapsule(bytes.NewReader(half)); err == nil {
		t.Fatal("不完整 capsule 应返回错误")
	}
}

func TestParseOneCapsulePartial(t *testing.T) {
	full := AppendCapsule(nil, CapsuleRouteAdvertisement, []byte{4, 10, 0, 0, 0, 10, 255, 255, 255, 0})
	if _, n, ok, err := parseOneCapsule(full[:3]); err != nil || ok || n != 0 {
		t.Fatalf("不完整数据不应解析成功：ok=%v n=%d err=%v", ok, n, err)
	}
	capsule, n, ok, err := parseOneCapsule(full)
	if err != nil || !ok || n != len(full) || capsule.Type != CapsuleRouteAdvertisement {
		t.Fatalf("完整 capsule 解析失败：%+v n=%d ok=%v err=%v", capsule, n, ok, err)
	}
}

// TestE2EMockServer 起一个最小 HTTP/3 CONNECT-IP 服务端（quic-go http3.Server），
// 验证客户端：CONNECT-IP 握手、ADDRESS_ASSIGN、HTTP Datagram 双向收发。
func TestE2EMockServer(t *testing.T) {
	cert := newTestCert(t)
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("监听 UDP 失败：%v", err)
	}
	assigned := net.ParseIP("10.88.7.2").To4()
	serverCtx, cancelServer := context.WithCancel(context.Background())
	defer cancelServer()

	h3srv := &http3.Server{
		TLSConfig:       &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h3"}},
		EnableDatagrams: true,
	}
	h3srv.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Proto != "connect-ip" {
			http.Error(w, "expect connect-ip", http.StatusBadRequest)
			return
		}
		streamer, ok := w.(http3.HTTPStreamer)
		if !ok {
			http.Error(w, "no streamer", http.StatusInternalServerError)
			return
		}
		stream := streamer.HTTPStream()
		w.Header().Set("Capsule-Protocol", "?1")
		w.WriteHeader(http.StatusOK)
		// 下发地址
		assignPayload, _ := EncodeAddressAssign([]AssignedAddress{{RequestID: 1, IP: assigned, PrefixLen: 24}})
		if _, err := w.Write(AppendCapsule(nil, CapsuleAddressAssign, assignPayload)); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 读客户端 capsule（ADDRESS_REQUEST 等），忽略内容。
		go func() {
			for {
				if _, err := ReadCapsule(r.Body); err != nil {
					return
				}
			}
		}()
		// 回显 DATAGRAM
		for {
			select {
			case <-serverCtx.Done():
				return
			default:
			}
			payload, err := stream.ReceiveDatagram(r.Context())
			if err != nil {
				return
			}
			_ = stream.SendDatagram(payload)
		}
	})
	go func() {
		_ = h3srv.Serve(udpConn)
	}()
	defer func() {
		_ = h3srv.Close()
	}()

	endpoint := udpConn.LocalAddr().String()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, DialConfig{
		Endpoint: endpoint,
		SNI:      "localhost",
		Token:    "test-token",
		Path:     DefaultPath,
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("MASQUE Dial 失败：%v", err)
	}
	defer conn.Close()

	waitCtx, cancelWait := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelWait()
	if err := conn.WaitAssigned(waitCtx); err != nil {
		t.Fatalf("等待 ADDRESS_ASSIGN 失败：%v", err)
	}
	got := conn.Assigned()
	if len(got) == 0 || !got[0].IP.Equal(assigned) || got[0].PrefixLen != 24 {
		t.Fatalf("ADDRESS_ASSIGN 结果异常：%+v", got)
	}

	// 发送请求（可选路径）不应报错。
	if err := conn.SendAddressRequest([]RequestedAddress{{RequestID: 1, IP: assigned, PrefixLen: 32}}); err != nil {
		t.Fatalf("发送 ADDRESS_REQUEST 失败：%v", err)
	}

	// DATAGRAM 往返
	pkt := makeIPv4UDP([]byte{0xde, 0xad, 0xbe, 0xef})
	if err := conn.SendPacket(pkt); err != nil {
		t.Fatalf("SendPacket 失败：%v", err)
	}
	type result struct {
		pkt []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		p, err := conn.RecvPacket()
		ch <- result{p, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("RecvPacket 失败：%v", r.err)
		}
		if !bytes.Equal(r.pkt, pkt) {
			t.Fatalf("DATAGRAM 回显不一致：got %x want %x", r.pkt, pkt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待 DATAGRAM 回显超时")
	}
	up, down := conn.Stats()
	t.Logf("masque 统计：dropped_up=%d dropped_down=%d", up, down)
}

func makeIPv4UDP(payload []byte) []byte {
	pkt := make([]byte, 20+8+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = 17
	copy(pkt[12:16], net.ParseIP("10.88.7.2").To4())
	copy(pkt[16:20], net.ParseIP("1.1.1.1").To4())
	binary.BigEndian.PutUint16(pkt[20:22], 12345)
	binary.BigEndian.PutUint16(pkt[22:24], 53)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(8+len(payload)))
	copy(pkt[28:], payload)
	return pkt
}

func newTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("生成密钥失败：%v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("签发测试证书失败：%v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

var _ = io.EOF
