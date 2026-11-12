package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 验证 4 种 PTR 状态能区分开：ok / provider_default / forward_mismatch / missing，
// 以及查询失败时的 unknown。
func TestClassifyPTRStates(t *testing.T) {
	okForward := func(string) ([]string, error) { return []string{"23.254.172.198"}, nil }
	mismatchForward := func(string) ([]string, error) {
		return []string{"172.67.138.128", "104.21.64.253"}, nil
	}
	emptyForward := func(string) ([]string, error) { return nil, nil }
	failForward := func(string) ([]string, error) { return nil, errors.New("lookup failed") }

	cases := []struct {
		name    string
		ip      string
		names   []string
		lookErr error
		forward func(string) ([]string, error)
		want    string
		substr  string
	}{
		{
			name: "forward confirmed", ip: "23.254.172.198",
			names: []string{"mail-proxypilot.yangzifun.org."}, forward: okForward,
			want: ptrStatusOK, substr: "23.254.172.198 → mail-proxypilot.yangzifun.org",
		},
		{
			// VPS 服务商默认主机名：不能当成「已配置」，也不能当成「无 PTR」。
			name: "provider default hostname", ip: "23.254.172.198",
			names: []string{"racknerd-b3c2b64."}, forward: okForward,
			want: ptrStatusProviderDefault, substr: "服务商默认主机名",
		},
		{
			// PTR 指向被 CDN 代理的域名：PTR 有，但正查回不到本机 IP。
			name: "forward mismatch (cdn proxied)", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: mismatchForward,
			want: ptrStatusForwardMismatch, substr: "forward-confirmed 不成立",
		},
		{
			name: "forward has no records", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: emptyForward,
			want: ptrStatusForwardMismatch, substr: "没有 A/AAAA",
		},
		{
			name: "forward lookup error", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: failForward,
			want: ptrStatusForwardMismatch, substr: "正查失败",
		},
		{
			name: "no ptr", ip: "23.254.172.198", forward: okForward,
			want: ptrStatusMissing, substr: "无 PTR",
		},
		{
			name: "lookup error", ip: "23.254.172.198", lookErr: errors.New("timeout"), forward: okForward,
			want: ptrStatusUnknown, substr: "PTR 查询失败",
		},
		{
			// IPv4-mapped IPv6 写法也要判定为同一个地址
			name: "ipv4 mapped compare", ip: "23.254.172.198",
			names: []string{"mail.example.com."},
			forward: func(string) ([]string, error) {
				return []string{"::ffff:23.254.172.198"}, nil
			},
			want: ptrStatusOK, substr: "mail.example.com",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, actual := classifyPTR(c.ip, c.names, c.lookErr, c.forward)
			if got != c.want {
				t.Fatalf("状态 = %q，期望 %q（说明：%s）", got, c.want, actual)
			}
			if c.substr != "" && !strings.Contains(actual, c.substr) {
				t.Fatalf("说明里缺少 %q：%s", c.substr, actual)
			}
		})
	}
}

// 反查域名拼接（IPv4 / IPv6）。
func TestDNSReverseName(t *testing.T) {
	got, err := dnsReverseName("23.254.172.198")
	if err != nil || got != "198.172.254.23.in-addr.arpa" {
		t.Fatalf("IPv4 反查名 = %q, %v", got, err)
	}
	got, err = dnsReverseName("2607:9d00:2000:1b7::e592:6851")
	// 与 Python 的 ipaddress.ip_address(...).reverse_pointer 对齐。
	want := "1.5.8.6.2.9.5.e.0.0.0.0.0.0.0.0.7.b.1.0.0.0.0.2.0.0.d.9.7.0.6.2.ip6.arpa"
	if err != nil || got != want {
		t.Fatalf("IPv6 反查名 = %q, %v；期望 %q", got, err, want)
	}
	if _, err := dnsReverseName("not-an-ip"); err == nil {
		t.Fatal("非法 IP 应该报错")
	}
}

// fakeDNS 是一个最小 UDP DNS 服务器，用于验证 lookupPTRDirect / lookupHostDirect
// 真的走 DNS（而不是 /etc/hosts 或本地缓存）。
type fakeDNS struct {
	conn    net.PacketConn
	mu      sync.Mutex
	answers map[string][]string
}

func startFakeDNS(t *testing.T) (*fakeDNS, string) {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听 UDP 失败：%v", err)
	}
	f := &fakeDNS{conn: conn, answers: map[string][]string{}}
	go f.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return f, conn.LocalAddr().String()
}

func (f *fakeDNS) set(qtype uint16, name string, values ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[fmt.Sprintf("%d|%s", qtype, strings.ToLower(name))] = values
}

func (f *fakeDNS) lookup(qtype uint16, name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers[fmt.Sprintf("%d|%s", qtype, strings.ToLower(name))]
}

func (f *fakeDNS) serve() {
	buf := make([]byte, 1024)
	for {
		n, addr, err := f.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if resp := f.handle(buf[:n]); resp != nil {
			_, _ = f.conn.WriteTo(resp, addr)
		}
	}
}

func (f *fakeDNS) handle(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	off := 12
	var labels []string
	for {
		if off >= len(query) {
			return nil
		}
		l := int(query[off])
		if l == 0 {
			off++
			break
		}
		if off+1+l > len(query) {
			return nil
		}
		labels = append(labels, string(query[off+1:off+1+l]))
		off += 1 + l
	}
	if off+4 > len(query) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(query[off:])
	off += 4
	name := strings.Join(labels, ".")
	values := f.lookup(qtype, name)

	resp := []byte{query[0], query[1], 0x81, 0x80, 0x00, 0x01}
	resp = append(resp, 0x00, byte(len(values)))
	resp = append(resp, 0x00, 0x00, 0x00, 0x00)
	resp = append(resp, query[12:off]...)
	for _, v := range values {
		resp = append(resp, 0xC0, 0x0C) // 用压缩指针指回问题里的名字
		resp = append(resp, byte(qtype>>8), byte(qtype), 0x00, 0x01, 0x00, 0x00, 0x00, 0x3C)
		var rdata []byte
		switch qtype {
		case dnsTypePTR:
			for _, label := range strings.Split(strings.TrimSuffix(v, "."), ".") {
				if label == "" || len(label) > 63 {
					return nil
				}
				rdata = append(rdata, byte(len(label)))
				rdata = append(rdata, label...)
			}
			rdata = append(rdata, 0x00)
		case dnsTypeA:
			rdata = net.ParseIP(v).To4()
		case dnsTypeAAAA:
			rdata = net.ParseIP(v).To16()
		}
		if len(rdata) == 0 {
			return nil
		}
		resp = append(resp, byte(len(rdata)>>8), byte(len(rdata)))
		resp = append(resp, rdata...)
	}
	return resp
}

// 核心回归：反查必须能拿到 DNS 里的 PTR（不能受 /etc/hosts 影响），
// 正查必须能拿到 A 记录用于 forward-confirmed 判断。
func TestLookupPTRDirectUsesDNS(t *testing.T) {
	f, addr := startFakeDNS(t)
	f.set(dnsTypePTR, "198.172.254.23.in-addr.arpa", "mail-proxypilot.yangzifun.org.")
	f.set(dnsTypeA, "mail-proxypilot.yangzifun.org", "23.254.172.198")

	defer stubMailDNS([]string{addr}, "http://127.0.0.1:1/dns-query")()

	names, err := lookupPTRDirect("23.254.172.198")
	if err != nil {
		t.Fatalf("反查失败：%v", err)
	}
	if len(names) != 1 || names[0] != "mail-proxypilot.yangzifun.org" {
		t.Fatalf("反查结果 = %v", names)
	}
	addrs, err := lookupHostDirect("mail-proxypilot.yangzifun.org")
	if err != nil {
		t.Fatalf("正查失败：%v", err)
	}
	if len(addrs) != 1 || addrs[0] != "23.254.172.198" {
		t.Fatalf("正查结果 = %v", addrs)
	}
	// 没有 PTR 的 IP：应返回空结果且不报错（= 确实没有）
	names, err = lookupPTRDirect("192.0.2.55")
	if err != nil {
		t.Fatalf("空反查不应报错：%v", err)
	}
	if len(names) != 0 {
		t.Fatalf("空反查结果 = %v", names)
	}
	// 集成一遍状态判定：假 DNS 里 PTR 名正查能回到本机 IP，应为 ok
	status, actual := classifyPTR("23.254.172.198", []string{"mail-proxypilot.yangzifun.org."}, nil, mailPTRForwardLookup)
	if status != ptrStatusOK {
		t.Fatalf("状态 = %q（%s），期望 %q", status, actual, ptrStatusOK)
	}
}

// UDP 53 不可用时，必须能兜底到 DoH。
func TestMailDNSQueryFallsBackToDoH(t *testing.T) {
	var gotName, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("name")
		gotType = r.URL.Query().Get("type")
		w.Header().Set("content-type", "application/dns-json")
		_, _ = io.WriteString(w, `{"Status":0,"Answer":[{"name":"198.172.254.23.in-addr.arpa","type":12,"ttl":300,"data":"mail-proxypilot.yangzifun.org."}]}`)
	}))
	defer srv.Close()

	defer stubMailDNS(nil, srv.URL)()

	names, err := mailDNSQuery("198.172.254.23.in-addr.arpa", dnsTypePTR)
	if err != nil {
		t.Fatalf("DoH 兜底失败：%v", err)
	}
	if len(names) != 1 || names[0] != "mail-proxypilot.yangzifun.org" {
		t.Fatalf("DoH 结果 = %v", names)
	}
	if gotName != "198.172.254.23.in-addr.arpa" || gotType != "12" {
		t.Fatalf("DoH 请求参数不对：name=%q type=%q", gotName, gotType)
	}
}

// stubMailDNS 临时替换 DNS 上游 / DoH 端点，返回恢复函数。
func stubMailDNS(upstreams []string, doh string) func() {
	oldUpstreams, oldDoH, oldTimeout := mailDNSUpstreams, mailDoHEndpoint, mailDNSQueryTimeout
	mailDNSUpstreams = upstreams
	mailDoHEndpoint = doh
	mailDNSQueryTimeout = 800 * time.Millisecond
	return func() {
		mailDNSUpstreams, mailDoHEndpoint, mailDNSQueryTimeout = oldUpstreams, oldDoH, oldTimeout
	}
}
