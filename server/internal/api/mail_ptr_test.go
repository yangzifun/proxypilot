package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- PTR 分类（新语义：只看反查原始值；FCrDNS 只进 Detail 附注） ----

// 用户要求「获取 PTR 原始值，而不是正向解析」：
// 有 PTR 且是域名 = ok，即使 FCrDNS 不成立（域名被 Cloudflare 代理）也不能算未配置。
func TestClassifyPTRStates(t *testing.T) {
	okForward := func(string) ([]string, error) { return []string{"23.254.172.198"}, nil }
	mismatchForward := func(string) ([]string, error) {
		return []string{"172.67.138.128", "104.21.64.253"}, nil
	}
	emptyForward := func(string) ([]string, error) { return nil, nil }
	failForward := func(string) ([]string, error) { return nil, errors.New("lookup failed") }
	mappedForward := func(string) ([]string, error) {
		return []string{"::ffff:23.254.172.198"}, nil
	}

	cases := []struct {
		name    string
		ip      string
		names   []string
		lookErr error
		forward func(string) ([]string, error)
		want    string
		actual  string
		substr  string
	}{
		{
			name: "FQDN PTR + FCrDNS 一致", ip: "23.254.172.198",
			names: []string{"mail-proxypilot.yangzifun.org."}, forward: okForward,
			want: ptrStatusOK, actual: "mail-proxypilot.yangzifun.org", substr: "正反向一致",
		},
		{
			// 关键新语义：PTR 指向被 Cloudflare 代理的域名，正查回不到本机，
			// 但 PTR 本身确实存在且是域名 → 仍必须是 ok，只把 FCrDNS 写进附注。
			name: "FQDN PTR 但 FCrDNS 不成立仍是 ok", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: mismatchForward,
			want: ptrStatusOK, actual: "proxypilot.yangzifun.org", substr: "不含本机 IP",
		},
		{
			name: "FQDN PTR 正查无 A 记录仍是 ok", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: emptyForward,
			want: ptrStatusOK, actual: "proxypilot.yangzifun.org", substr: "没有 A/AAAA",
		},
		{
			name: "FQDN PTR 正查失败仍是 ok", ip: "23.254.172.198",
			names: []string{"proxypilot.yangzifun.org."}, forward: failForward,
			want: ptrStatusOK, actual: "proxypilot.yangzifun.org", substr: "正查",
		},
		{
			// VPS 服务商默认主机名：不能当成「已设置域名」。
			name: "服务商默认主机名", ip: "23.254.172.198",
			names: []string{"racknerd-b3c2b64."}, forward: okForward,
			want: ptrStatusProviderDefault, actual: "racknerd-b3c2b64", substr: "服务商默认主机名",
		},
		{
			name: "完全没有 PTR", ip: "23.254.172.198", forward: okForward,
			want: ptrStatusMissing, actual: "", substr: "没有 PTR",
		},
		{
			name: "只有空字符串的 PTR 答案", ip: "23.254.172.198",
			names: []string{" . ", "."}, forward: okForward,
			want: ptrStatusMissing, actual: "", substr: "没有 PTR",
		},
		{
			name: "所有 DNS 通道失败", ip: "23.254.172.198", lookErr: errors.New("timeout"), forward: okForward,
			want: ptrStatusUnknown, actual: "", substr: "PTR 查询失败",
		},
		{
			name: "多个 PTR 保留原始顺序", ip: "23.254.172.198",
			names: []string{"a.example.com.", "b.example.com."}, forward: okForward,
			want: ptrStatusOK, actual: "a.example.com, b.example.com", substr: "正反向一致",
		},
		{
			// IPv4-mapped IPv6 写法也要判定为同一个地址
			name: "IPv4-mapped IPv6 比较", ip: "23.254.172.198",
			names: []string{"mail.example.com."}, forward: mappedForward,
			want: ptrStatusOK, actual: "mail.example.com", substr: "正反向一致",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyPTR(c.ip, c.names, c.lookErr, c.forward)
			if got.Status != c.want {
				t.Fatalf("状态 = %q，期望 %q（Detail：%s）", got.Status, c.want, got.Detail)
			}
			if got.Actual != c.actual {
				t.Fatalf("Actual = %q，期望 %q", got.Actual, c.actual)
			}
			if c.substr != "" && !strings.Contains(got.Detail, c.substr) {
				t.Fatalf("Detail 里缺少 %q：%s", c.substr, got.Detail)
			}
		})
	}
}

// 注入 lookup：即使本机 /etc/hosts 里该 IP 已有别的名字（例如 127.0.0.1 localhost），
// 结果也必须来自注入的 DNS 查询函数，而不是系统解析器（NSS）。
func TestCheckPTRUsesInjectedLookupBypassingHosts(t *testing.T) {
	lookup := func(ip string) ([]string, error) {
		if ip != "127.0.0.1" {
			t.Fatalf("反查收到的 IP = %q", ip)
		}
		return []string{"mail.example.test."}, nil
	}
	forward := func(string) ([]string, error) { return []string{"127.0.0.1"}, nil }

	got := checkPTRFor("127.0.0.1", lookup, forward)
	// 本机 /etc/hosts 里 127.0.0.1 一定映射 localhost；如果实现走 NSS 会拿到 localhost。
	if got.Status != ptrStatusOK || got.Actual != "mail.example.test" {
		t.Fatalf("注入 lookup 未被使用：status=%q actual=%q detail=%s", got.Status, got.Actual, got.Detail)
	}
	if !strings.Contains(got.Detail, "正反向一致") {
		t.Fatalf("FCrDNS 附注缺失：%s", got.Detail)
	}

	// 探测不到出口 IP：unknown，且不能误报 missing。
	if got := checkPTRFor("", lookup, forward); got.Status != ptrStatusUnknown {
		t.Fatalf("空 IP 状态 = %q，期望 %q", got.Status, ptrStatusUnknown)
	}
	// 查询函数为 nil 时同样未知。
	if got := checkPTRFor("23.254.172.198", nil, forward); got.Status != ptrStatusUnknown {
		t.Fatalf("nil lookup 状态 = %q，期望 %q", got.Status, ptrStatusUnknown)
	}
}

// PTR 记录展示：Host 是真实反查名、Actual 是原始 PTR、缺省时 Value 给建议目标。
func TestBuildPTRRecord(t *testing.T) {
	ok := buildPTRRecord("23.254.172.198", "proxypilot.yangzifun.org", ptrVerdict{
		Status: ptrStatusOK, Actual: "proxypilot.yangzifun.org", Detail: "FCrDNS 附注（仅参考）",
	})
	if ok.Host != "198.172.254.23.in-addr.arpa" {
		t.Fatalf("PTR 记录的 Host = %q", ok.Host)
	}
	if ok.Actual != "proxypilot.yangzifun.org" {
		t.Fatalf("PTR 记录的 Actual = %q（应保留原始 PTR 值）", ok.Actual)
	}
	if !strings.Contains(ok.Value, "23.254.172.198") || !strings.Contains(ok.Value, "proxypilot.yangzifun.org") {
		t.Fatalf("PTR 记录的 Value = %q", ok.Value)
	}
	if ok.Detail != "FCrDNS 附注（仅参考）" {
		t.Fatalf("PTR 记录的 Detail = %q", ok.Detail)
	}

	missing := buildPTRRecord("23.254.172.198", "proxypilot.yangzifun.org", ptrVerdict{Status: ptrStatusMissing})
	if missing.Status != ptrStatusMissing || missing.Actual != "" {
		t.Fatalf("无 PTR 记录：status=%q actual=%q", missing.Status, missing.Actual)
	}
	if !strings.Contains(missing.Value, "mail.proxypilot.yangzifun.org") {
		t.Fatalf("无 PTR 时应建议 mail.<domain>：%q", missing.Value)
	}

	// 拿不到出口 IP 时 Host 退回提示文案，不能崩。
	unknown := buildPTRRecord("", "proxypilot.yangzifun.org", ptrVerdict{Status: ptrStatusUnknown})
	if !strings.Contains(unknown.Host, "VPS 服务商控制台") {
		t.Fatalf("无出口 IP 时的 Host = %q", unknown.Host)
	}
}

// ---- MX 解析 / 展示 ----

// 用 fake UDP DNS 验证 MX 是「直接查 DNS 拿原始值」，并验证按 priority 排序。
func TestLookupMXDirectUsesDNS(t *testing.T) {
	f, addr := startFakeDNS(t)
	f.set(dnsTypeMX, "proxypilot.yangzifun.org",
		"71 route3.mx.cloudflare.net",
		"10 mail-proxypilot.yangzifun.org",
		"62 route1.mx.cloudflare.net",
	)
	defer stubMailDNS([]string{addr}, "http://127.0.0.1:1/dns-query")()

	got, err := lookupMXDirect("proxypilot.yangzifun.org.")
	if err != nil {
		t.Fatalf("MX 查询失败：%v", err)
	}
	want := []mxRecord{
		{Preference: 10, Host: "mail-proxypilot.yangzifun.org"},
		{Preference: 62, Host: "route1.mx.cloudflare.net"},
		{Preference: 71, Host: "route3.mx.cloudflare.net"},
	}
	if len(got) != len(want) {
		t.Fatalf("MX 结果 = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("MX[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}

	// 域名确实没有 MX：返回空且不报错（状态是 missing，不是 unknown）。
	got, err = lookupMXDirect("mail.yangzifun.org")
	if err != nil {
		t.Fatalf("空 MX 不应报错：%v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空 MX 结果 = %v", got)
	}
}

// MX 报文解析：含压缩指针、NXDOMAIN、非法 rcode、报文过短。
func TestParseDNSMXAnswers(t *testing.T) {
	msg := buildMXResponse()
	got, err := parseDNSMXAnswers(msg)
	if err != nil {
		t.Fatalf("解析 MX 响应失败：%v", err)
	}
	if len(got) != 2 || got[0].Preference != 10 || got[0].Host != "mail-proxypilot.yangzifun.org" {
		t.Fatalf("MX 解析结果 = %v", got)
	}
	// 第二条的 exchange 用压缩指针指回问题名
	if got[1].Preference != 20 || got[1].Host != "proxypilot.yangzifun.org" {
		t.Fatalf("压缩指针解析 = %v", got[1])
	}

	if _, err := parseDNSMXAnswers([]byte{1, 2, 3}); err == nil {
		t.Fatal("过短报文应报错")
	}
	servfail := make([]byte, 12)
	servfail[3] = 0x02 // rcode=2 SERVFAIL
	if _, err := parseDNSMXAnswers(servfail); err == nil {
		t.Fatal("SERVFAIL 应报错（状态会显示查询失败，而不是误报没有 MX）")
	}
	nxdomain := make([]byte, 12)
	nxdomain[3] = 0x03 // rcode=3 NXDOMAIN，按「没有记录」处理
	if out, err := parseDNSMXAnswers(nxdomain); err != nil || len(out) != 0 {
		t.Fatalf("NXDOMAIN 应返回空且不报错：%v %v", out, err)
	}
}

// DoH 兜底也要能解析 MX（UDP 53 被拦截时）。
func TestMailDNSQueryMXFallsBackToDoH(t *testing.T) {
	var gotName, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotName = r.URL.Query().Get("name")
		gotType = r.URL.Query().Get("type")
		w.Header().Set("content-type", "application/dns-json")
		_, _ = io.WriteString(w, `{"Status":0,"Answer":[`+
			`{"name":"proxypilot.yangzifun.org","type":15,"ttl":300,"data":"20 backup.yangzifun.org."},`+
			`{"name":"proxypilot.yangzifun.org","type":15,"ttl":300,"data":"10 mail-proxypilot.yangzifun.org."}]}`)
	}))
	defer srv.Close()

	defer stubMailDNS(nil, srv.URL)()

	got, err := mailDNSQueryMX("proxypilot.yangzifun.org")
	if err != nil {
		t.Fatalf("DoH MX 兜底失败：%v", err)
	}
	if len(got) != 2 || got[0].Preference != 10 || got[1].Preference != 20 {
		t.Fatalf("DoH MX 结果 = %v（应按 priority 升序）", got)
	}
	if gotName != "proxypilot.yangzifun.org" || gotType != "15" {
		t.Fatalf("DoH 请求参数不对：name=%q type=%q", gotName, gotType)
	}
}

// MX 记录展示：Actual 是真实 MX 原始值；Detail 说明是否指向本机、是否与 PTR 一致；
// 缺失时 Value 给建议值；查询失败是 unknown 而不是 missing。
func TestBuildMXRecord(t *testing.T) {
	ptrOK := ptrVerdict{Status: ptrStatusOK, Actual: "mail-proxypilot.yangzifun.org"}
	ptrMismatch := ptrVerdict{Status: ptrStatusOK, Actual: "proxypilot.yangzifun.org"}
	resolve := func(host string) ([]string, error) {
		switch strings.ToLower(host) {
		case "mail-proxypilot.yangzifun.org":
			return []string{"23.254.172.198"}, nil
		case "route1.mx.cloudflare.net":
			return []string{"172.67.1.1"}, nil
		}
		return nil, errors.New("nxdomain")
	}
	mx := mxLookup{Records: []mxRecord{
		{Preference: 10, Host: "mail-proxypilot.yangzifun.org"},
		{Preference: 62, Host: "route1.mx.cloudflare.net"},
	}}

	rec := buildMXRecord("proxypilot.yangzifun.org", "23.254.172.198", ptrOK, mx, resolve)
	if rec.Status != ptrStatusOK {
		t.Fatalf("状态 = %q（%s）", rec.Status, rec.Detail)
	}
	if rec.Actual != "10 mail-proxypilot.yangzifun.org; 62 route1.mx.cloudflare.net" {
		t.Fatalf("Actual 原始 MX 值 = %q", rec.Actual)
	}
	if rec.Value != rec.Actual {
		t.Fatalf("MX 已存在时 Value 应展示真实值：value=%q actual=%q", rec.Value, rec.Actual)
	}
	for _, want := range []string{"指向本机（23.254.172.198）：mail-proxypilot.yangzifun.org", "不指向本机：route1.mx.cloudflare.net", "与 PTR 主机名一致"} {
		if !strings.Contains(rec.Detail, want) {
			t.Fatalf("Detail 缺少 %q：%s", want, rec.Detail)
		}
	}

	// PTR 是另一个域名 → 一致性附注要指出不一致，但 MX 状态仍是 ok。
	rec = buildMXRecord("proxypilot.yangzifun.org", "23.254.172.198", ptrMismatch, mx, resolve)
	if rec.Status != ptrStatusOK || !strings.Contains(rec.Detail, "与 PTR 主机名不一致") {
		t.Fatalf("PTR 不一致场景：status=%q detail=%s", rec.Status, rec.Detail)
	}

	// 没有 MX：missing，并给建议值（10 mail.<domain>）。
	rec = buildMXRecord("proxypilot.yangzifun.org", "23.254.172.198", ptrOK, mxLookup{}, resolve)
	if rec.Status != ptrStatusMissing {
		t.Fatalf("无 MX 状态 = %q", rec.Status)
	}
	if rec.Value != "10 mail.proxypilot.yangzifun.org" || rec.Actual != "" {
		t.Fatalf("无 MX 时的 Value/Actual = %q / %q", rec.Value, rec.Actual)
	}
	if !strings.Contains(rec.Detail, "建议值") || !strings.Contains(rec.Detail, "23.254.172.198") {
		t.Fatalf("无 MX 时的 Detail = %s", rec.Detail)
	}

	// 查询失败：unknown（不能误报缺失）。
	rec = buildMXRecord("proxypilot.yangzifun.org", "23.254.172.198", ptrOK, mxLookup{Err: errors.New("timeout")}, resolve)
	if rec.Status != ptrStatusUnknown || !strings.Contains(rec.Detail, "MX 查询失败") {
		t.Fatalf("MX 查询失败场景：status=%q detail=%s", rec.Status, rec.Detail)
	}
}

// MX 排序：priority 升序 + 去重，priority 数值必须保留。
func TestSortMXRecords(t *testing.T) {
	in := []mxRecord{
		{Preference: 30, Host: "c.example.com"},
		{Preference: 10, Host: "a.example.com"},
		{Preference: 10, Host: "A.example.com."}, // 与上一条不同（大小写/结尾点不统一），保留
		{Preference: 10, Host: "a.example.com"},  // 完全重复，去掉
		{Preference: 20, Host: ""},               // 空主机名，去掉
		{Preference: 20, Host: "b.example.com"},
	}
	got := sortMXRecords(in)
	if len(got) != 4 {
		t.Fatalf("排序去重结果 = %v", got)
	}
	if got[0].Preference != 10 || got[1].Preference != 10 || got[2].Preference != 20 || got[3].Preference != 30 {
		t.Fatalf("priority 未升序：%v", got)
	}
}

// ---- 反查域名 / DNS 查询底层（保留原有回归） ----

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

// fakeDNS 是一个最小 UDP DNS 服务器，用于验证 lookupPTRDirect / lookupMXDirect
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
			rdata = appendDNSName(nil, v)
		case dnsTypeA:
			rdata = net.ParseIP(v).To4()
		case dnsTypeAAAA:
			rdata = net.ParseIP(v).To16()
		case dnsTypeMX:
			fields := strings.SplitN(strings.TrimSpace(v), " ", 2)
			if len(fields) != 2 {
				return nil
			}
			pref, perr := strconv.Atoi(fields[0])
			if perr != nil {
				return nil
			}
			rdata = append([]byte{byte(pref >> 8), byte(pref)}, appendDNSName(nil, fields[1])...)
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
// 正查必须能拿到 A 记录用于 FCrDNS 附注。
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
	// 集成一遍状态判定：假 DNS 里 PTR 是域名 → ok，实际值就是原始 PTR。
	got := classifyPTR("23.254.172.198", []string{"mail-proxypilot.yangzifun.org."}, nil, mailPTRForwardLookup)
	if got.Status != ptrStatusOK || got.Actual != "mail-proxypilot.yangzifun.org" {
		t.Fatalf("状态 = %q（%s），期望 %q", got.Status, got.Detail, ptrStatusOK)
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

// buildMXResponse 手工构造一个含两条 MX 答案的响应报文（第二条 exchange 用压缩指针）。
func buildMXResponse() []byte {
	msg := []byte{0x12, 0x34, 0x81, 0x80, 0x00, 0x01, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00}
	msg = appendDNSName(msg, "proxypilot.yangzifun.org")
	msg = append(msg, 0x00, 0x0F, 0x00, 0x01) // MX, IN
	// answer 1：name 指回问题名，rdata = pref 10 + mail-proxypilot.yangzifun.org
	msg = append(msg, 0xC0, 0x0C, 0x00, 0x0F, 0x00, 0x01, 0x00, 0x00, 0x01, 0x2C)
	rdata := appendDNSName([]byte{0x00, 0x0A}, "mail-proxypilot.yangzifun.org")
	msg = append(msg, byte(len(rdata)>>8), byte(len(rdata)))
	msg = append(msg, rdata...)
	// answer 2：name 指回问题名，rdata = pref 20 + 压缩指针 0xC00C（指回问题名）
	msg = append(msg, 0xC0, 0x0C, 0x00, 0x0F, 0x00, 0x01, 0x00, 0x00, 0x01, 0x2C)
	msg = append(msg, 0x00, 0x04, 0x00, 0x14, 0xC0, 0x0C)
	return msg
}

// appendDNSName 把域名按 DNS 线格式（长度前缀 + 终止 0）追加到 buf。
func appendDNSName(buf []byte, name string) []byte {
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		buf = append(buf, byte(len(label)))
		buf = append(buf, label...)
	}
	return append(buf, 0x00)
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
