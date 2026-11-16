package node

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- 测试工具 ----

func masqueTestServer(t *testing.T) (*Server, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewVerifier(base64.StdEncoding.EncodeToString(pub), "proxypilot", "pp-tunnel")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	v.SetClock(func() time.Time { return now })
	return &Server{
		cfg: Config{
			NodeID: 5, ClientCIDR: "10.88.3.0/24", Gateway4: "10.88.3.1",
			MTU: 1280, MaxDatagram: 1460,
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		verifier: v,
		sessions: make(map[string]*sessionSet),
	}, priv
}

func masqueTestClaims(t *testing.T, priv ed25519.PrivateKey, ip string) string {
	t.Helper()
	return signToken(t, priv, Claims{
		Issuer: "proxypilot", Audience: "pp-tunnel", Subject: "device:9",
		NodeID: 5, GrantID: 2, IPv4: ip, IssuedAt: 1_800_000_000,
		ExpiresAt: 1_800_000_600, JTI: "j-masque",
	})
}

func masqueTestRequest(t *testing.T, priv ed25519.PrivateKey, path string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodConnect, path, nil)
	req.Host = "node.example"
	req.Proto = "connect-ip"
	req.Header.Set("Authorization", "Bearer "+masqueTestClaims(t, priv, "10.88.3.10"))
	req.Header.Set("Capsule-Protocol", "?1")
	return req
}

// ---- 路径解析 ----

func TestMasqueParsePath(t *testing.T) {
	ok := []struct {
		path    string
		target  string
		ipproto string
	}{
		{"/.well-known/masque/ip/*/*/", "*", "*"},
		{"/.well-known/masque/ip/*/*", "*", "*"},
		{"/.well-known/masque/ip/0.0.0.0%2F0/*/", "0.0.0.0%2F0", "*"},
		{"/.well-known/masque/ip/203.0.113.7%2F32/17/", "203.0.113.7%2F32", "17"},
		{"/.well-known/masque/ip/proxy.example.com/6", "proxy.example.com", "6"},
		{"/.well-known/masque/ip/*/0", "*", "0"},
	}
	for _, tc := range ok {
		target, ipproto, err := parseMasquePath(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if target != tc.target || ipproto != tc.ipproto {
			t.Fatalf("%s => (%q,%q)，期望 (%q,%q)", tc.path, target, ipproto, tc.target, tc.ipproto)
		}
	}
	bad := []string{
		"/", "/masque/ip/*/*/", "/.well-known/masque/", "/.well-known/masque/ip/",
		"/.well-known/masque/ip//*/", "/.well-known/masque/ip/*/300/",
		"/.well-known/masque/ip/*/-1/", "/.well-known/masque/ip/*/udp/",
	}
	for _, p := range bad {
		if _, _, err := parseMasquePath(p); err == nil {
			t.Fatalf("%s 应解析失败", p)
		}
	}
}

// ---- capsule 编解码 ----

func TestMasqueAddressCapsuleRoundTrip(t *testing.T) {
	ip := net.ParseIP("10.88.3.10").To4()
	in := []masqueAddress{
		{RequestID: 0, IP: ip, PrefixLen: 32},
		{RequestID: 7, IP: net.IPv4zero.To4(), PrefixLen: 0},
	}
	raw, err := marshalAddressList(in)
	if err != nil {
		t.Fatal(err)
	}
	// 第一个 Assigned Address：requestID=0(0x00), version=4, 4 字节地址, prefix=32(0x20)。
	if raw[0] != 0x00 || raw[1] != 4 {
		t.Fatalf("地址头字节 = % x", raw[:6])
	}
	if net.IP(raw[2:6]).To4().String() != "10.88.3.10" || raw[6] != 32 {
		t.Fatalf("地址编码错误：% x", raw[:7])
	}
	out, err := parseAddressList(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("解析出 %d 条", len(out))
	}
	if out[0].RequestID != 0 || !out[0].IP.Equal(ip) || out[0].PrefixLen != 32 {
		t.Fatalf("第 1 条 = %+v", out[0])
	}
	if out[1].RequestID != 7 || out[1].PrefixLen != 0 || !out[1].IP.Equal(net.IPv4zero) {
		t.Fatalf("第 2 条 = %+v", out[1])
	}

	// IPv6 往返
	v6 := net.ParseIP("2001:db8::1")
	raw6, err := marshalAddressList([]masqueAddress{{RequestID: 9, IP: v6, PrefixLen: 128}})
	if err != nil {
		t.Fatal(err)
	}
	out6, err := parseAddressList(raw6)
	if err != nil || len(out6) != 1 || !out6[0].IP.Equal(v6) || out6[0].PrefixLen != 128 {
		t.Fatalf("IPv6 往返失败：%+v %v", out6, err)
	}

	// 空列表合法（表示撤销全部地址）。
	if out, err := parseAddressList(nil); err != nil || len(out) != 0 {
		t.Fatalf("空列表应合法：%v %v", out, err)
	}
}

func TestMasqueAddressCapsuleMalformed(t *testing.T) {
	cases := [][]byte{
		{0x00},                       // 缺 version
		{0x00, 4, 10, 88, 3},         // 缺地址字节/前缀
		{0x00, 5, 0, 0, 0, 0, 32},    // 非法 version
		{0x00, 4, 10, 88, 3, 10, 33}, // 前缀 > 32
		{0x00, 4, 10, 88, 3, 10, 24}, // /24 下低位非 0
	}
	for i, raw := range cases {
		if _, err := parseAddressList(raw); err == nil {
			t.Fatalf("malformed #%d 应失败：% x", i, raw)
		}
	}
	// marshal 校验前缀长度。
	if _, err := marshalAddressList([]masqueAddress{{IP: net.ParseIP("10.0.0.1").To4(), PrefixLen: 33}}); err == nil {
		t.Fatal("前缀 33 应被拒绝")
	}
}

func TestMasqueRouteAdvertisementRoundTrip(t *testing.T) {
	routes := []masqueRoute{
		{Start: net.IPv4zero.To4(), End: net.IPv4bcast.To4(), Protocol: 0},
		{Start: net.ParseIP("203.0.113.0").To4(), End: net.ParseIP("203.0.113.255").To4(), Protocol: 6},
	}
	raw, err := marshalRouteAdvertisement(routes)
	if err != nil {
		t.Fatal(err)
	}
	out, err := parseRouteAdvertisement(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("解析出 %d 条路由", len(out))
	}
	if !out[0].Start.Equal(net.IPv4zero) || !out[0].End.Equal(net.IPv4bcast) || out[0].Protocol != 0 {
		t.Fatalf("默认路由 = %+v", out[0])
	}
	if !out[1].Start.Equal(net.ParseIP("203.0.113.0")) || out[1].Protocol != 6 {
		t.Fatalf("第二条路由 = %+v", out[1])
	}

	// IPv6 路由
	v6routes := []masqueRoute{{Start: net.ParseIP("2001:db8::"), End: net.ParseIP("2001:db8::ffff"), Protocol: 0}}
	raw6, err := marshalRouteAdvertisement(v6routes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRouteAdvertisement(raw6); err != nil {
		t.Fatalf("IPv6 路由解析失败：%v", err)
	}
}

func TestMasqueRouteAdvertisementMalformed(t *testing.T) {
	// 截断
	if _, err := parseRouteAdvertisement([]byte{4, 0, 0, 0}); err == nil {
		t.Fatal("截断路由应失败")
	}
	// start > end
	raw, err := marshalRouteAdvertisement([]masqueRoute{{
		Start: net.ParseIP("203.0.113.9").To4(), End: net.ParseIP("203.0.113.1").To4(),
	}})
	if err == nil {
		t.Fatalf("start>end 应拒绝：% x", raw)
	}
	// 同协议重叠：手工构造两条 0.0.0.0-255.255.255.255 与 10.0.0.0-10.0.0.255
	var b []byte
	for _, rt := range []masqueRoute{
		{Start: net.IPv4zero.To4(), End: net.IPv4bcast.To4(), Protocol: 0},
		{Start: net.ParseIP("10.0.0.0").To4(), End: net.ParseIP("10.0.0.255").To4(), Protocol: 0},
	} {
		b = append(b, 4)
		b = append(b, rt.Start.To4()...)
		b = append(b, rt.End.To4()...)
		b = append(b, rt.Protocol)
	}
	if _, err := parseRouteAdvertisement(b); err == nil {
		t.Fatal("重叠路由应失败")
	}
}

func TestMasqueCapsuleFraming(t *testing.T) {
	var buf bytes.Buffer
	value := []byte("hello-capsule")
	if err := writeCapsule(&buf, capsuleRouteAdvertisement, value); err != nil {
		t.Fatal(err)
	}
	ct, got, err := readCapsule(bufio.NewReader(bytes.NewReader(buf.Bytes())))
	if err != nil {
		t.Fatal(err)
	}
	if ct != capsuleRouteAdvertisement || !bytes.Equal(got, value) {
		t.Fatalf("capsule 往返失败：type=%d value=%q", ct, got)
	}

	// 超长 length：varint 编码 2^20，应被拒绝。
	var bad []byte
	bad = append(bad, 0x01)
	bad = appendVarintForTest(bad, masqueMaxCapsuleLen+1)
	if _, _, err := readCapsule(bufio.NewReader(bytes.NewReader(bad))); err == nil {
		t.Fatal("超长 capsule 应失败")
	}
	// 截断 header。
	if _, _, err := readCapsule(bufio.NewReader(bytes.NewReader([]byte{0x01}))); err == nil {
		t.Fatal("截断 capsule 头应失败")
	}
}

// appendVarintForTest 只用于构造非法输入（直接复用 quicvarint 的编码规则）。
func appendVarintForTest(b []byte, v uint64) []byte {
	if v <= 63 {
		return append(b, byte(v))
	}
	if v <= 16383 {
		return append(b, byte(v>>8)|0x40, byte(v))
	}
	return append(b, byte(v>>24)|0x80, byte(v>>16), byte(v>>8), byte(v))
}

func TestMasqueDatagramPayload(t *testing.T) {
	pkt := []byte{0x45, 0, 0, 28, 0, 0, 0, 0, 64, 1, 0, 0, 10, 88, 3, 10, 1, 2, 3, 4}
	payload := masqueDatagramPayload(pkt)
	// Context ID 0 编码为单字节 0x00。
	if payload[0] != 0x00 {
		t.Fatalf("Context ID 首字节 = %#x", payload[0])
	}
	if !bytes.Equal(payload[1:], pkt) {
		t.Fatal("IP 报文被改写")
	}
	ctxID, data, err := parseMasqueDatagram(payload)
	if err != nil || ctxID != 0 || !bytes.Equal(data, pkt) {
		t.Fatalf("解析失败：ctx=%d data=% x err=%v", ctxID, data, err)
	}

	// 非 0 Context ID 解析后应保留（由调用方丢弃）。
	var other []byte
	other = append(other, 0x41) // 2 字节 varint：0x41 0x00 = 256
	other = append(other, 0x00)
	other = append(other, pkt...)
	ctxID, data, err = parseMasqueDatagram(other)
	if err != nil || ctxID != 256 || !bytes.Equal(data, pkt) {
		t.Fatalf("非 0 Context ID 解析失败：ctx=%d err=%v", ctxID, err)
	}
	if _, _, err := parseMasqueDatagram(nil); err == nil {
		t.Fatal("空载荷应失败")
	}
}

func TestMasqueIPSource(t *testing.T) {
	pkt := make([]byte, 24)
	pkt[0] = 0x45
	pkt[2], pkt[3] = 0, 24
	copy(pkt[12:16], net.ParseIP("10.88.3.10").To4())
	copy(pkt[16:20], net.ParseIP("1.1.1.1").To4())
	src, err := masqueIPSource(pkt)
	if err != nil || !src.Equal(net.ParseIP("10.88.3.10")) {
		t.Fatalf("源地址 = %v err=%v", src, err)
	}
	if src.Equal(net.ParseIP("10.88.3.11")) {
		t.Fatal("源地址不应等于其它 IP")
	}

	if _, err := masqueIPSource(nil); err == nil {
		t.Fatal("短包应失败")
	}
	truncated := append([]byte(nil), pkt...)
	truncated[2], truncated[3] = 0, 60 // total 超过实际长度
	if _, err := masqueIPSource(truncated); err == nil {
		t.Fatal("total length 超出应失败")
	}
	v6 := make([]byte, 40)
	v6[0] = 0x60
	if _, err := masqueIPSource(v6); err == nil {
		t.Fatal("IPv6 应返回暂不支持")
	}
}

// ---- 鉴权 ----

func TestMasqueAuthorize(t *testing.T) {
	s, priv := masqueTestServer(t)
	ip := net.ParseIP("10.88.3.10").To4()

	// 1) 无 Authorization → 401
	req := httptest.NewRequest(http.MethodConnect, "/.well-known/masque/ip/*/*/", nil)
	req.Host = "node.example"
	if _, _, status, _ := s.masqueAuthorize(req); status != http.StatusUnauthorized {
		t.Fatalf("无 token status=%d", status)
	}
	// 2) 坏 token → 401
	req = masqueTestRequest(t, priv, "/.well-known/masque/ip/*/*/")
	req.Header.Set("Authorization", "Bearer bad.token.here")
	if _, _, status, _ := s.masqueAuthorize(req); status != http.StatusUnauthorized {
		t.Fatalf("坏 token status=%d", status)
	}
	// 3) 节点 ID 不匹配 → 403
	badNode := signToken(t, priv, Claims{
		Issuer: "proxypilot", Audience: "pp-tunnel", Subject: "device:9",
		NodeID: 6, IPv4: "10.88.3.10", IssuedAt: 1_800_000_000,
		ExpiresAt: 1_800_000_600, JTI: "j-bad-node",
	})
	req = masqueTestRequest(t, priv, "/.well-known/masque/ip/*/*/")
	req.Header.Set("Authorization", "Bearer "+badNode)
	if _, _, status, _ := s.masqueAuthorize(req); status != http.StatusForbidden {
		t.Fatalf("错误节点 status=%d", status)
	}
	// 4) IP 不在 CIDR → 403
	outside := signToken(t, priv, Claims{
		Issuer: "proxypilot", Audience: "pp-tunnel", Subject: "device:9",
		NodeID: 5, IPv4: "10.99.0.5", IssuedAt: 1_800_000_000,
		ExpiresAt: 1_800_000_600, JTI: "j-outside",
	})
	req = masqueTestRequest(t, priv, "/.well-known/masque/ip/*/*/")
	req.Header.Set("Authorization", "Bearer "+outside)
	if _, _, status, _ := s.masqueAuthorize(req); status != http.StatusForbidden {
		t.Fatalf("越界 IP status=%d", status)
	}
	// 5) 正常 token
	claims, gotIP, status, msg := s.masqueAuthorize(masqueTestRequest(t, priv, "/.well-known/masque/ip/*/*/"))
	if status != 0 || claims == nil || !gotIP.Equal(ip) {
		t.Fatalf("正常鉴权失败：status=%d msg=%q claims=%+v ip=%v", status, msg, claims, gotIP)
	}
}

func TestServeHTTPMasqueRejects(t *testing.T) {
	s, priv := masqueTestServer(t)

	// 路径不是 MASQUE 模板 → 404（CONNECT connect-ip）
	req := masqueTestRequest(t, priv, "/tunnel")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("错误路径 status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 正常路径但无 token → 401
	req = httptest.NewRequest(http.MethodConnect, "/.well-known/masque/ip/*/*/", nil)
	req.Host = "node.example"
	req.Proto = "connect-ip"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token status=%d body=%s", rec.Code, rec.Body.String())
	}
	// 普通 GET /（私有协议）不受影响：无 token 仍 401
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "node.example"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("私有 GET / status=%d", rec.Code)
	}
	// 普通 GET /tunnel（私有协议）仍走 serveStream 的 405
	req = httptest.NewRequest(http.MethodGet, "/tunnel", nil)
	req.Host = "node.example"
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("私有 GET /tunnel status=%d", rec.Code)
	}
}

// ---- 会话选择 ----

func TestSessionSetMasquePick(t *testing.T) {
	ip := net.ParseIP("10.88.3.10").To4()
	msess := &session{kind: kindMasque, ip: ip, send: func([]byte) error { return nil }}
	ssess := &session{kind: kindStream, ip: ip, send: func([]byte) error { return nil }}
	dsess := &session{kind: kindDatagram, ip: ip, send: func([]byte) error { return nil }}

	// MASQUE 独占：所有协议都回 MASQUE
	only := &sessionSet{masque: msess}
	for _, proto := range []byte{1, 6, 17} {
		if got := only.pick(mkPkt(proto)); got != msess {
			t.Fatalf("proto=%d 应选 MASQUE", proto)
		}
	}
	// 残留私有会话时仍优先 MASQUE（避免协议切换期间下行发到旧通道）
	mixed := &sessionSet{masque: msess, stream: ssess, datagram: dsess}
	for _, proto := range []byte{1, 6, 17} {
		if got := mixed.pick(mkPkt(proto)); got != msess {
			t.Fatalf("mixed proto=%d 应选 MASQUE", proto)
		}
	}
	// MASQUE 是独占承载，没有可回退的私有通道
	if alt := only.other(msess); alt != nil {
		t.Fatalf("MASQUE other 应为 nil，得到 %+v", alt)
	}
	// 无 MASQUE 的私有集合行为不变（回归）
	if got := (&sessionSet{stream: ssess, datagram: dsess}).pick(mkPkt(17)); got != dsess {
		t.Fatal("私有 UDP 仍应走 DATAGRAM")
	}
	if got := (&sessionSet{stream: ssess, datagram: dsess}).pick(mkPkt(6)); got != ssess {
		t.Fatal("私有 TCP 仍应走 STREAM")
	}
}

func TestSessionSetMasqueRegisterUnregister(t *testing.T) {
	srv := newSessionSetTestServer()
	ip := net.ParseIP("10.88.3.10").To4()
	msess := newTestSession(ip, kindMasque)
	if !srv.register(msess) {
		t.Fatal("MASQUE 注册失败")
	}
	set := srv.lookup(ip)
	if set == nil || set.masque != msess {
		t.Fatal("masque 槽位未注册")
	}
	// 私有会话与 MASQUE 并存（互不接管）
	ssess := newTestSession(ip, kindStream)
	if !srv.register(ssess) {
		t.Fatal("stream 注册失败")
	}
	set = srv.lookup(ip)
	if set.masque != msess || set.stream != ssess {
		t.Fatal("MASQUE 与私有会话应并存")
	}
	// 注销 MASQUE 后私有仍在；全部注销才删记录
	srv.unregister(msess)
	set = srv.lookup(ip)
	if set == nil || set.masque != nil || set.stream != ssess {
		t.Fatal("注销 MASQUE 后私有会话应保留")
	}
	srv.unregister(ssess)
	if srv.lookup(ip) != nil {
		t.Fatal("全部通道注销后记录应删除")
	}
}

func TestSessionSetMasqueTakeoverClosesOld(t *testing.T) {
	srv := newSessionSetTestServer()
	ip := net.ParseIP("10.88.3.10").To4()
	closed := 0
	old := newTestSession(ip, kindMasque)
	old.closeFn = func() { closed++ }
	srv.register(old)
	next := newTestSession(ip, kindMasque)
	srv.register(next)
	set := srv.lookup(ip)
	if set.masque != next {
		t.Fatal("同 kind 新 MASQUE 会话应接管")
	}
	if closed != 1 {
		t.Fatalf("旧 MASQUE 会话 closeFn 调用 %d 次，期望 1", closed)
	}
}

func TestMasqueMTU(t *testing.T) {
	s := &Server{cfg: Config{MTU: 1400}}
	if got := s.masqueMTU(); got != 1400 {
		t.Fatalf("MTU=%d", got)
	}
	s.cfg.MTU = 1000
	if got := s.masqueMTU(); got != masqueDefaultMTU {
		t.Fatalf("低于 1280 应提示 1280，得到 %d", got)
	}
	s.cfg.MTU = 0
	if got := s.masqueMTU(); got != masqueDefaultMTU {
		t.Fatalf("未配置应回退 1280，得到 %d", got)
	}
}

// TestMasquePathRejectsOtherConnect 保证非 connect-ip 的 CONNECT 不会被 MASQUE 接管。
func TestMasquePathRejectsOtherConnect(t *testing.T) {
	s, priv := masqueTestServer(t)
	req := httptest.NewRequest(http.MethodConnect, "/.well-known/masque/ip/*/*/", nil)
	req.Host = "node.example"
	req.Proto = "connect-udp"
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("connect-udp 不应进 MASQUE：status=%d", rec.Code)
	}
	// 明确带 Connect-Protocol: connect-ip 头（兼容 r.Proto 未被设置的实现）。
	// 鉴权通过后没有 QUIC 连接 → 400（datagram 未协商），证明已进入 MASQUE 分支。
	req = masqueTestRequest(t, priv, "/.well-known/masque/ip/*/*/")
	req.Proto = "HTTP/3.0"
	req.Header.Set("Connect-Protocol", "connect-ip")
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("兼容头 status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// ---- 上行校验和规范化（task-34：修「tx 增长但 rx=0」） ----

func testIPPacket(src, dst [4]byte, proto byte, l4 []byte) []byte {
	total := 20 + len(l4)
	pkt := make([]byte, total)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(total))
	pkt[8] = 64
	pkt[9] = proto
	copy(pkt[12:16], src[:])
	copy(pkt[16:20], dst[:])
	copy(pkt[20:], l4)
	binary.BigEndian.PutUint16(pkt[10:12], ipv4HeaderChecksum(pkt[:20]))
	return pkt
}

func testUDPPacket(src, dst [4]byte, sport, dport uint16, payload []byte, zeroChecksum bool) []byte {
	udp := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(udp[0:2], sport)
	binary.BigEndian.PutUint16(udp[2:4], dport)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], payload)
	pkt := testIPPacket(src, dst, 17, udp)
	if !zeroChecksum {
		setL4Checksum(pkt, 20, 17, 6)
	}
	return pkt
}

func testTCPPacket(src, dst [4]byte, sport, dport uint16) []byte {
	tcp := make([]byte, 20)
	binary.BigEndian.PutUint16(tcp[0:2], sport)
	binary.BigEndian.PutUint16(tcp[2:4], dport)
	tcp[12] = 5 << 4
	tcp[13] = 0x02
	pkt := testIPPacket(src, dst, 6, tcp)
	setL4Checksum(pkt, 20, 6, 16)
	return pkt
}

func testICMPPacket(src, dst [4]byte, seq uint16) []byte {
	icmp := make([]byte, 8)
	icmp[0] = 8
	binary.BigEndian.PutUint16(icmp[4:6], seq)
	pkt := testIPPacket(src, dst, 1, icmp)
	setL4Checksum(pkt, 20, 1, 2)
	return pkt
}

func TestNormalizeMasqueUplink(t *testing.T) {
	src := [4]byte{10, 88, 3, 10}
	dst := [4]byte{10, 88, 3, 1}
	dns := []byte{0x12, 0x34, 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0, 3, 'w', 'w', 'w', 0, 0, 1, 0, 1}

	// 1) 合法报文：不做任何修改。
	valid := testUDPPacket(src, dst, 40000, 53, dns, false)
	out, fix, err := normalizeMasqueUplink(valid)
	if err != nil || fix != (masqueUplinkFix{}) {
		t.Fatalf("合法报文不应修改：fix=%+v err=%v", fix, err)
	}
	if !bytes.Equal(out, valid) {
		t.Fatal("合法报文内容被改写")
	}
	if !ipv4HeaderChecksumOK(out[:20]) || !l4ChecksumOK(out, 20, 17) {
		t.Fatal("合法报文校验和异常")
	}

	// 1b) 合法 TCP / ICMP 也不应被修改（避免校验和重算误伤）。
	validTCP := testTCPPacket(src, [4]byte{1, 1, 1, 1}, 40007, 443)
	if _, fix, err := normalizeMasqueUplink(validTCP); err != nil || fix != (masqueUplinkFix{}) {
		t.Fatalf("合法 TCP 不应修改：fix=%+v err=%v", fix, err)
	}
	validICMP := testICMPPacket(src, dst, 9)
	if _, fix, err := normalizeMasqueUplink(validICMP); err != nil || fix != (masqueUplinkFix{}) {
		t.Fatalf("合法 ICMP 不应修改：fix=%+v err=%v", fix, err)
	}

	// 2) IPv4 头校验和为 0（校验和卸载/手工构造常见）→ 修正，L4 不动。
	zeroIP := testUDPPacket(src, dst, 40001, 53, dns, false)
	binary.BigEndian.PutUint16(zeroIP[10:12], 0)
	out, fix, err = normalizeMasqueUplink(zeroIP)
	if err != nil || !fix.FixedL3 || fix.FixedL4 {
		t.Fatalf("IPv4 校验和 0 应被修正：fix=%+v err=%v", fix, err)
	}
	if !ipv4HeaderChecksumOK(out[:20]) || out[0] != 0x45 {
		t.Fatal("IPv4 校验和修正失败")
	}

	// 3) IPv4 头校验和错误 → 修正。
	badIP := testUDPPacket(src, dst, 40002, 53, dns, false)
	binary.BigEndian.PutUint16(badIP[10:12], 0xdead)
	out, fix, err = normalizeMasqueUplink(badIP)
	if err != nil || !fix.FixedL3 || !ipv4HeaderChecksumOK(out[:20]) {
		t.Fatalf("IPv4 校验和错误应被修正：fix=%+v err=%v", fix, err)
	}

	// 4) UDP 校验和错误 → 修正。
	badUDP := testUDPPacket(src, dst, 40003, 53, dns, false)
	binary.BigEndian.PutUint16(badUDP[26:28], 0xbeef)
	out, fix, err = normalizeMasqueUplink(badUDP)
	if err != nil || !fix.FixedL4 || fix.FixedL3 {
		t.Fatalf("UDP 校验和错误应被修正：fix=%+v err=%v", fix, err)
	}
	if !l4ChecksumOK(out, 20, 17) {
		t.Fatal("UDP 校验和修正失败")
	}

	// 5) UDP 校验和 0 是 IPv4 合法值：保留 0，不触发修正。
	zeroUDP := testUDPPacket(src, dst, 40004, 53, dns, true)
	out, fix, err = normalizeMasqueUplink(zeroUDP)
	if err != nil || fix.FixedL4 {
		t.Fatalf("UDP 校验和 0 不应被改：fix=%+v err=%v", fix, err)
	}
	if binary.BigEndian.Uint16(out[26:28]) != 0 {
		t.Fatal("UDP 校验和 0 被改写")
	}

	// 6) TCP 校验和错误 → 修正。
	tcp := testTCPPacket(src, [4]byte{1, 1, 1, 1}, 40005, 443)
	binary.BigEndian.PutUint16(tcp[36:38], 0x1111)
	out, fix, err = normalizeMasqueUplink(tcp)
	if err != nil || !fix.FixedL4 || !l4ChecksumOK(out, 20, 6) {
		t.Fatalf("TCP 校验和错误应被修正：fix=%+v err=%v", fix, err)
	}

	// 7) ICMP 校验和错误 → 修正。
	icmp := testICMPPacket(src, dst, 7)
	binary.BigEndian.PutUint16(icmp[22:24], 0x2222)
	out, fix, err = normalizeMasqueUplink(icmp)
	if err != nil || !fix.FixedL4 || !l4ChecksumOK(out, 20, 1) {
		t.Fatalf("ICMP 校验和错误应被修正：fix=%+v err=%v", fix, err)
	}

	// 8) 分片包（MF）：不碰 L4，只标记 Fragmented。
	frag := testUDPPacket(src, dst, 40006, 53, dns, false)
	binary.BigEndian.PutUint16(frag[6:8], 0x2000)
	binary.BigEndian.PutUint16(frag[26:28], 0xbeef)
	out, fix, err = normalizeMasqueUplink(frag)
	if err != nil || !fix.Fragmented || fix.FixedL4 {
		t.Fatalf("分片包应跳过 L4 修正：fix=%+v err=%v", fix, err)
	}
	if binary.BigEndian.Uint16(out[26:28]) != 0xbeef {
		t.Fatal("分片包的 L4 字段不应被改写")
	}

	// 9) 尾部多余字节：按 IP 总长度裁剪。
	pad := append(append([]byte(nil), valid...), 0xaa, 0xbb, 0xcc)
	out, _, err = normalizeMasqueUplink(pad)
	if err != nil || len(out) != len(valid) {
		t.Fatalf("尾部填充未裁剪：len=%d err=%v", len(out), err)
	}
}

func TestNormalizeMasqueUplinkRejects(t *testing.T) {
	src := [4]byte{10, 88, 3, 10}
	dst := [4]byte{10, 88, 3, 1}
	dns := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	valid := testUDPPacket(src, dst, 40000, 53, dns, false)

	cases := map[string][]byte{
		"空/过短":      {0x45, 0, 0},
		"IPv6":      {0x60, 0, 0, 0},
		"IHL 超长":    func() []byte { p := append([]byte(nil), valid...); p[0] = 0x4f; return p }(),
		"总长度小于 IHL": func() []byte { p := append([]byte(nil), valid...); binary.BigEndian.PutUint16(p[2:4], 10); return p }(),
		"总长度超过实际长度": func() []byte { p := append([]byte(nil), valid...); binary.BigEndian.PutUint16(p[2:4], 200); return p }(),
		"UDP 长度非法":  func() []byte { p := append([]byte(nil), valid...); binary.BigEndian.PutUint16(p[24:26], 4); return p }(),
		"TCP 头长度非法": func() []byte {
			p := testTCPPacket(src, dst, 1, 443)
			p[32] = 2 << 4 // data offset = 8 < 20
			return p
		}(),
	}
	for name, pkt := range cases {
		if _, _, err := normalizeMasqueUplink(pkt); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		}
	}
}

func TestMasqueDebugEnv(t *testing.T) {
	for _, v := range []string{"1", "true", "on", "YES", "debug"} {
		if !parseDebugEnv(v) {
			t.Fatalf("%q 应视为开启", v)
		}
	}
	for _, v := range []string{"", "0", "false", "off", "no"} {
		if parseDebugEnv(v) {
			t.Fatalf("%q 应视为关闭", v)
		}
	}
}
