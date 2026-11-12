package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DKIM 签名（rsa-sha256，relaxed/relaxed）。
//
// 自建直投没有 ESP 帮忙签名，SPF 又只能证明「IP 被允许」，
// 少了 DKIM 的邮件在 Gmail/QQ 基本进垃圾箱。这里在面板内自己签。

const dkimDefaultSelector = "pp"

// dkimMaxSingleString 是 DNS TXT 单个字符-字符串的上限（RFC 1035）。
// 超过就会被拆段，个别收件方只读首段 → DKIM 校验失败。
const dkimMaxSingleString = 255

// dkimKeyPair 是生成结果：私钥存库（加密），公钥拼进 DNS TXT。
type dkimKeyPair struct {
	PrivatePEM string
	PublicB64  string
	Bits       int
}

// generateDKIMKey 生成 RSA 密钥。
//
// bits=2048 是安全推荐；bits=1024 用于「兼容模式」——
// 2048 位公钥 base64 后约 360 字符，TXT 记录会被拆成 2 个字符-字符串，
// 部分收件方（例如某些国内邮件网关）只读第一个 255 字节段，导致
// 「DKIM signature error」。1024 位整条记录约 233 字符，能放进单段。
func generateDKIMKey(bits int) (dkimKeyPair, error) {
	if bits != 1024 && bits != 2048 {
		bits = 2048
	}
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return dkimKeyPair{}, fmt.Errorf("生成 DKIM 私钥失败：%w", err)
	}
	privDER := x509.MarshalPKCS1PrivateKey(key)
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: privDER})

	// TXT 记录里放 PKCS#1 RSAPublicKey 的 base64（DKIM 最常见的形式）
	pubDER := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	return dkimKeyPair{
		PrivatePEM: string(privPEM),
		PublicB64:  base64.StdEncoding.EncodeToString(pubDER),
		Bits:       bits,
	}, nil
}

// dkimPublicFromPrivate 从私钥 PEM 反推 TXT 记录里的公钥串。
func dkimPublicFromPrivate(privatePEM string) (string, error) {
	key, err := parseRSAPrivateKey(privatePEM)
	if err != nil {
		return "", err
	}
	pubDER := x509.MarshalPKCS1PublicKey(&key.PublicKey)
	return base64.StdEncoding.EncodeToString(pubDER), nil
}

func parseRSAPrivateKey(privatePEM string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privatePEM))
	if block == nil {
		return nil, fmt.Errorf("DKIM 私钥格式无效")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := k.(*rsa.PrivateKey); ok {
			return rk, nil
		}
	}
	return nil, fmt.Errorf("DKIM 私钥无法解析")
}

// 参与签名的头，按此顺序写入 h=。
var dkimSignedHeaders = []string{
	"from", "to", "subject", "date", "mime-version", "content-type", "content-transfer-encoding",
}

// dkimSign 在 message 顶部追加 DKIM-Signature 头并返回完整报文。
//
// message 必须是「头 + 空行 + 正文」且换行为 CRLF。
func dkimSign(message []byte, domain, selector, privatePEM string) ([]byte, error) {
	if strings.TrimSpace(privatePEM) == "" {
		return message, nil
	}
	key, err := parseRSAPrivateKey(privatePEM)
	if err != nil {
		return nil, err
	}

	head, body := splitMessage(message)
	headerMap := parseHeaderMap(head)

	// 正文规范化（relaxed）：去掉行尾空白、折叠连续空行到单个、末尾只留一个 CRLF
	canonBody := canonBodyRelaxed(body)
	bodyHash := sha256.Sum256(canonBody)
	bh := base64.StdEncoding.EncodeToString(bodyHash[:])

	// 只签报文里真实存在的头，避免 h= 里有不存在的字段导致验证失败
	present := make([]string, 0, len(dkimSignedHeaders))
	for _, name := range dkimSignedHeaders {
		if strings.TrimSpace(headerMap[name]) != "" {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		return message, nil
	}

	dkimHeader := "DKIM-Signature: v=1; a=rsa-sha256; c=relaxed/relaxed;\r\n" +
		"\td=" + domain + "; s=" + selector + ";\r\n" +
		"\tt=" + fmt.Sprint(time.Now().Unix()) + ";\r\n" +
		"\th=" + strings.Join(present, ":") + ";\r\n" +
		"\tbh=" + bh + ";\r\n" +
		"\tb="

	// 签名输入 = 各头的 relaxed 规范化 + DKIM-Signature 自身（b= 为空）
	var toSign bytes.Buffer
	for _, name := range present {
		toSign.WriteString(canonHeaderRelaxed(name, headerMap[name]))
	}
	toSign.WriteString(canonDKIMHeader(dkimHeader))

	digest := sha256.Sum256(toSign.Bytes())
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("DKIM 签名失败：%w", err)
	}

	full := dkimHeader + base64.StdEncoding.EncodeToString(sig) + "\r\n"
	out := make([]byte, 0, len(full)+len(message))
	out = append(out, full...)
	out = append(out, message...)
	return out, nil
}

// splitMessage 按第一个空行切分头与正文。
func splitMessage(msg []byte) (head string, body []byte) {
	s := string(msg)
	if i := strings.Index(s, "\r\n\r\n"); i >= 0 {
		return s[:i], []byte(s[i+4:])
	}
	if i := strings.Index(s, "\n\n"); i >= 0 {
		return s[:i], []byte(s[i+2:])
	}
	return s, nil
}

// parseHeaderMap 解析头，同名头只保留最后一个（简化处理，本项目的头都是唯一的）。
func parseHeaderMap(head string) map[string]string {
	out := map[string]string{}
	var cur string
	for _, line := range strings.Split(head, "\r\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if cur != "" {
				out[cur] += "\r\n" + line
			}
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 {
			continue
		}
		cur = strings.ToLower(strings.TrimSpace(line[:i]))
		out[cur] = line[i+1:]
	}
	return out
}

var wspRun = regexp.MustCompile(`[ \t]+`)

// canonHeaderRelaxed 按 RFC 6376 3.4.2 规范化单个头。
func canonHeaderRelaxed(name, value string) string {
	v := strings.ReplaceAll(value, "\r\n", "\n")
	v = strings.ReplaceAll(v, "\n", "") // 展开折行
	v = wspRun.ReplaceAllString(v, " ")
	v = strings.TrimSpace(v)
	return strings.ToLower(name) + ":" + v + "\r\n"
}

// canonDKIMHeader 规范化 DKIM-Signature 自身：值里的 WSP 折叠、去末尾 CRLF。
func canonDKIMHeader(h string) string {
	_, value := func() (string, string) {
		i := strings.Index(h, ":")
		return h[:i], h[i+1:]
	}()
	v := strings.ReplaceAll(value, "\r\n", "")
	v = wspRun.ReplaceAllString(v, " ")
	return "dkim-signature:" + strings.TrimSpace(v)
}

// canonBodyRelaxed 按 RFC 6376 3.4.4 规范化正文。
func canonBodyRelaxed(body []byte) []byte {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	// 去掉末尾空行
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return []byte("\r\n")
	}
	return []byte(strings.Join(lines, "\r\n") + "\r\n")
}

// dkimDNSRecord 生成 TXT 记录值。
//
// 返回的是「单行、无内部引号」的形式 —— 这正是 Cloudflare / 阿里云等控制台
// 表单要求的格式（它们自己负责按 255 字节切分）。
// 带内部分段引号的写法是 BIND 文件格式，粘进网页表单会报
// 「记录内容的引用方式不正确」。
func dkimDNSRecord(publicB64 string) string {
	return "v=DKIM1; k=rsa; p=" + strings.TrimSpace(publicB64)
}

// dkimDNSRecordBind 生成 BIND 文件格式（按 255 字符分段并各自加引号）。
// 用于「复制为 BIND 片段」导入 Cloudflare 的 Import and Export。
func dkimDNSRecordBind(publicB64 string) string {
	prefix := "v=DKIM1; k=rsa; p="
	all := prefix + strings.TrimSpace(publicB64)
	const maxLen = 255
	var parts []string
	for len(all) > maxLen {
		parts = append(parts, `"`+all[:maxLen]+`"`)
		all = all[maxLen:]
	}
	if all != "" {
		parts = append(parts, `"`+all+`"`)
	}
	return strings.Join(parts, " ")
}

// ---- DNS 指引 ----

type dnsRecord struct {
	Type   string `json:"type"`
	Host   string `json:"host"`
	Value  string `json:"value"`
	TTL    int    `json:"ttl"`
	Note   string `json:"note"`
	Status string `json:"status"` // ok / provider_default / missing / unknown
	Actual string `json:"actual"` // 真实查询到的原始值（PTR/MX 直接来自 DNS，便于排错）
	Detail string `json:"detail"` // 附加说明（FCrDNS、MX 是否指向本机等），不参与主状态判定
	Key    string `json:"key"`    // 记录标识，便于前端定位
}

// mailDNSGuide 生成发信所需的 DNS 记录指引，并实时校验。
//
// PTR 与 MX 一律「直接查 DNS 拿原始记录」：
//   - PTR：反查本机公网 IP 的 in-addr.arpa，展示原始 PTR 主机名；状态只看反查结果
//     （无 PTR / 服务商默认名 / 已设置域名），forward-confirmed 只写进 Detail 附注。
//   - MX：直接查发信域名的 MX（priority + exchange），不靠正向解析推断；是否指向
//     本机、是否与 PTR 主机名一致写进 Detail，缺失时在 Value 给出建议值。
func (h *handlers) mailDNSGuide(domain, selector, publicB64, spfExtra string) []dnsRecord {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return nil
	}
	if selector == "" {
		selector = dkimDefaultSelector
	}

	// SPF：把本机出口 IP 写进去（直投必需）
	spf := "v=spf1 mx ~all"
	if ip := strings.TrimSpace(spfExtra); ip != "" {
		spf = "v=spf1 a mx ip4:" + ip + " ~all"
	}

	recs := []dnsRecord{
		{
			Type: "TXT", Host: domain, TTL: 300, Key: "spf",
			Value: spf,
			Note:  "声明允许本机发信。ip4 为本面板服务器的公网出口地址；若也走中转，把中转的 include 一起写上。",
		},
		{
			Type: "TXT", Host: selector + "._domainkey." + domain, TTL: 300, Key: "dkim",
			Value: dkimDNSRecord(publicB64),
			Note:  "DKIM 公钥。长记录会自动拆成多个 255 字符片段，整体作为一条 TXT 提交。",
		},
		{
			Type: "TXT", Host: "_dmarc." + domain, TTL: 300, Key: "dmarc",
			Value: "v=DMARC1; p=none; rua=mailto:postmaster@" + domain,
			Note:  "刚开始用 p=none 观察，一周后确认没有误判再改成 p=quarantine。",
		},
	}

	// 实时校验 TXT
	for i := range recs {
		status, actual := h.checkTXT(recs[i].Host, recs[i].Key)
		recs[i].Status = status
		recs[i].Actual = actual
	}

	// PTR 与 MX 都是直查 DNS 的独立查询，并发执行以缩短邮件页等待。
	ip := h.mailEgressIP()
	ptrCh := make(chan ptrVerdict, 1)
	mxCh := make(chan mxLookup, 1)
	go func() { ptrCh <- checkPTRFor(ip, mailPTRLookup, mailPTRForwardLookup) }()
	go func() {
		list, err := mailMXLookup(domain)
		mxCh <- mxLookup{Records: list, Err: err}
	}()
	ptr := <-ptrCh
	mxRes := <-mxCh

	recs = append(recs, buildPTRRecord(ip, domain, ptr))
	recs = append(recs, buildMXRecord(domain, ip, ptr, mxRes, mailPTRForwardLookup))
	return recs
}

// checkTXT 查询某主机的 TXT 并按类型粗略比对。
func (h *handlers) checkTXT(host, kind string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	txts, err := net.DefaultResolver.LookupTXT(ctx, host)
	if err != nil || len(txts) == 0 {
		return "missing", ""
	}
	joined := strings.Join(txts, "|")
	switch kind {
	case "spf":
		for _, t := range txts {
			if strings.HasPrefix(strings.TrimSpace(t), "v=spf1") {
				return "ok", t
			}
		}
		return "missing", joined
	case "dkim":
		for _, t := range txts {
			if strings.Contains(t, "p=") {
				return "ok", shortText(t, 60)
			}
		}
		return "missing", shortText(joined, 60)
	case "dmarc":
		for _, t := range txts {
			if strings.HasPrefix(strings.TrimSpace(t), "v=DMARC1") {
				return "ok", t
			}
		}
		return "missing", joined
	}
	return "ok", joined
}

// ---- PTR / MX 原始值检测 ----
//
// 用户要求：面板要看到「PTR / MX 的真实记录值」，不能用正向解析去推断或当主判据。
//
// PTR 判定状态（只看反查原始值）：
//
//	ok               PTR 存在且是域名（FQDN，如 mail.example.com）
//	provider_default PTR 只是服务商默认主机名（不含域名后缀，如 racknerd-b3c2b64）
//	missing          完全没有 PTR
//	unknown          探测不到出口 IP，或所有 DNS 通道都不可用
//
// forward-confirmed（把 PTR 名字正查 A/AAAA 看是否回到本机）只作为 Detail 里的
// 附注参考，不参与状态判定 —— 域名被 Cloudflare 代理时 FCrDNS 天然不成立，
// 但这并不代表 PTR 没设置。
//
// 为什么不直接用 net.DefaultResolver：本机 nsswitch 是 "hosts: files dns"，
// Go 的 goLookupPTR 在 hostLookupFilesDNS 顺序下会先读 /etc/hosts
// （见 net/dnsclient_unix.go 的 lookupStaticAddr），而 VPS 服务商常把
// 「公网 IP 默认主机名」写进 /etc/hosts；另外 cloudflare-warp 的本地 DNS 会
// 缓存旧 PTR（实测 TTL 86400，而权威反解区是 300）。所以这里自己发 DNS 查询：
// 先直连公共 DNS（UDP 53），失败再兜底 DoH（HTTPS），两条路都完全绕开 NSS。
const (
	ptrStatusOK              = "ok"
	ptrStatusProviderDefault = "provider_default"
	ptrStatusMissing         = "missing"
	ptrStatusUnknown         = "unknown"
)

const (
	dnsTypeA    uint16 = 1
	dnsTypePTR  uint16 = 12
	dnsTypeMX   uint16 = 15
	dnsTypeAAAA uint16 = 28
)

// mailDNSUpstreams 是直连查询用的公共 DNS；可用环境变量
// PP_MAIL_DNS_UPSTREAMS（逗号分隔，如 "1.1.1.1:53,8.8.8.8:53"）覆盖。
var mailDNSUpstreams = mailDNSUpstreamsFromEnv()

func mailDNSUpstreamsFromEnv() []string {
	if v := strings.TrimSpace(os.Getenv("PP_MAIL_DNS_UPSTREAMS")); v != "" {
		var out []string
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"1.1.1.1:53", "8.8.8.8:53", "1.0.0.1:53"}
}

// mailDoHEndpoint 是直连 UDP 53 被拦截时的兜底（RFC 8484 JSON 模式）。
var mailDoHEndpoint = "https://cloudflare-dns.com/dns-query"

// mailDNSQueryTimeout 是单次 DNS 查询超时。
var mailDNSQueryTimeout = 3 * time.Second

// 以下变量便于测试注入（把 DNS 查询换成假实现，验证绕过 /etc/hosts）。
var (
	mailPTRLookup        = lookupPTRDirect
	mailPTRForwardLookup = lookupHostDirect
	mailMXLookup         = lookupMXDirect
)

// ptrVerdict 是 PTR 检查结果。
//
// Status 只由反查原始值决定；Actual 是原始 PTR 主机名；Detail 是附加说明
// （含 FCrDNS 附注），不参与状态判定。
type ptrVerdict struct {
	Status string
	Actual string
	Detail string
}

// checkPTR 检查本机公网 IP 的反向解析，返回原始 PTR 值。
func (h *handlers) checkPTR() ptrVerdict {
	return checkPTRFor(h.mailEgressIP(), mailPTRLookup, mailPTRForwardLookup)
}

// checkPTRFor 用可注入的查询函数做 PTR 检查（便于单测模拟 /etc/hosts 干扰）。
func checkPTRFor(ip string, lookup func(string) ([]string, error), forward func(string) ([]string, error)) ptrVerdict {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ptrVerdict{Status: ptrStatusUnknown, Detail: "未能探测公网出口 IP，无法反查 PTR"}
	}
	if lookup == nil {
		return ptrVerdict{Status: ptrStatusUnknown, Detail: "PTR 查询未配置"}
	}
	names, err := lookup(ip)
	return classifyPTR(ip, names, err, forward)
}

// classifyPTR 根据反查（names）结果给出状态与原始值。
// forwardLookup 只用于生成 FCrDNS 附注，不影响状态。抽成纯函数便于单元测试。
func classifyPTR(ip string, names []string, lookupErr error, forwardLookup func(string) ([]string, error)) ptrVerdict {
	if lookupErr != nil {
		return ptrVerdict{
			Status: ptrStatusUnknown,
			Detail: ip + " 的 PTR 查询失败（公共 DNS 与 DoH 都不可用）：" + lookupErr.Error(),
		}
	}
	var ptrs []string
	for _, n := range names {
		if n = strings.TrimSuffix(strings.TrimSpace(n), "."); n != "" {
			ptrs = append(ptrs, n)
		}
	}
	if len(ptrs) == 0 {
		return ptrVerdict{
			Status: ptrStatusMissing,
			Detail: ip + " 在公共 DNS 反查为空，即该 IP 没有 PTR 记录；需要到 VPS 服务商控制台添加",
		}
	}
	actual := strings.Join(ptrs, ", ")
	first := ptrs[0]
	// 服务商默认主机名（如 racknerd-b3c2b64）不带域名后缀，收件方基本不认。
	if !strings.Contains(first, ".") {
		return ptrVerdict{
			Status: ptrStatusProviderDefault,
			Actual: actual,
			Detail: ip + " 的 PTR 是 " + first + "（服务商默认主机名，不含域名后缀）；" +
				"收件方不认这种名字，建议到 VPS 服务商控制台改成发信域名",
		}
	}
	return ptrVerdict{
		Status: ptrStatusOK,
		Actual: actual,
		Detail: describeFCrDNS(ip, first, forwardLookup),
	}
}

// describeFCrDNS 生成 FCrDNS 附注：把 PTR 名字正查 A/AAAA，看是否回到本机 IP。
// 这里的结果只写进 Detail，不改变 PTR 状态（PTR 是否已设置只看反查原始值）。
func describeFCrDNS(ip, ptr string, forwardLookup func(string) ([]string, error)) string {
	if forwardLookup == nil {
		return ""
	}
	addrs, err := forwardLookup(ptr)
	if err != nil {
		return "FCrDNS 附注（仅参考，不影响 PTR 状态）：正查 " + ptr + " 失败（" + err.Error() + "）"
	}
	if len(addrs) == 0 {
		return "FCrDNS 附注（仅参考，不影响 PTR 状态）：" + ptr + " 没有 A/AAAA 记录"
	}
	for _, a := range addrs {
		if sameIP(a, ip) {
			return "FCrDNS 附注（仅参考，不影响 PTR 状态）：PTR 名 " + ptr + " 正查含本机 IP，正反向一致"
		}
	}
	return "FCrDNS 附注（仅参考，不影响 PTR 状态）：PTR 名 " + ptr + " 正查为 " +
		strings.Join(addrs, ", ") + "，不含本机 IP；PTR 本身已设置，但严格校验 FCrDNS 的收件方会扣分"
}

// buildPTRRecord 把 PTR 检查结果组装成面板展示用的记录。
// Actual 是反查到的原始 PTR 主机名（无记录时为空），Value 是建议的 PTR 目标。
func buildPTRRecord(egressIP, domain string, v ptrVerdict) dnsRecord {
	rec := dnsRecord{
		Type: "PTR", Host: "（在 VPS 服务商控制台设置）", TTL: 0, Key: "ptr",
		Status: v.Status,
		Actual: v.Actual,
		Detail: v.Detail,
	}
	// Host 直接显示真实反查名（in-addr.arpa），比一句「去服务商控制台设置」更可核对。
	if rev, err := dnsReverseName(egressIP); err == nil {
		rec.Host = rev
	}
	target := v.Actual
	if target == "" || !strings.Contains(target, ".") {
		target = "mail." + domain
	}
	if egressIP != "" {
		rec.Value = "面板服务器公网 IP " + egressIP + " → " + target
	} else {
		rec.Value = "面板服务器公网 IP → " + target
	}
	rec.Note = "反向解析（PTR）。这里显示的是本机直连公共 DNS 反查到 23.254.172.198 这类公网 IP 的原始 PTR 主机名，" +
		"不读 /etc/hosts、不用正向解析推断。PTR 只能在 VPS 服务商控制台设置（Cloudflare 不能给用户网段加 PTR）；" +
		"没有 PTR 的 IP 会被 Gmail/QQ 直接判垃圾。FCrDNS（PTR 名正查能否回到本机）只作附注参考。"
	return rec
}

// sameIP 判断两个 IP 字符串是否相同（兼容 IPv4 映射成 IPv6 的写法）。
func sameIP(a, b string) bool {
	ia := net.ParseIP(strings.TrimSpace(a))
	ib := net.ParseIP(strings.TrimSpace(b))
	return ia != nil && ib != nil && ia.Equal(ib)
}

// dnsReverseName 把 IP 转成反查域名（in-addr.arpa / ip6.arpa）。
func dnsReverseName(ip string) (string, error) {
	addr := net.ParseIP(strings.TrimSpace(ip))
	if addr == nil {
		return "", fmt.Errorf("IP 无效：%s", ip)
	}
	if v4 := addr.To4(); v4 != nil {
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", v4[3], v4[2], v4[1], v4[0]), nil
	}
	v6 := addr.To16()
	if v6 == nil {
		return "", fmt.Errorf("IP 无效：%s", ip)
	}
	nibbles := make([]string, 0, 32)
	for i := len(v6) - 1; i >= 0; i-- {
		nibbles = append(nibbles, fmt.Sprintf("%x", v6[i]&0x0F), fmt.Sprintf("%x", v6[i]>>4))
	}
	return strings.Join(nibbles, ".") + ".ip6.arpa", nil
}

// buildDNSQuery 构造一个标准递归查询报文（普通 UDP，不带 EDNS）。
func buildDNSQuery(name string, qtype uint16) ([]byte, uint16, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	if name == "" {
		return nil, 0, errors.New("DNS 查询名为空")
	}
	idBytes := make([]byte, 2)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, 0, err
	}
	id := uint16(idBytes[0])<<8 | uint16(idBytes[1])
	msg := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 {
			return nil, 0, fmt.Errorf("DNS 查询名不合法：%s", name)
		}
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0x00, byte(qtype>>8), byte(qtype), 0x00, 0x01)
	return msg, id, nil
}

// readDNSName 解析名字（支持压缩指针）。
func readDNSName(msg []byte, off int) (string, int, error) {
	var labels []string
	for {
		if off < 0 || off >= len(msg) {
			return "", 0, errors.New("DNS 报文越界")
		}
		l := int(msg[off])
		if l == 0 {
			return strings.Join(labels, "."), off + 1, nil
		}
		if l&0xC0 == 0xC0 {
			if off+1 >= len(msg) {
				return "", 0, errors.New("DNS 压缩指针越界")
			}
			ptr := int(msg[off]&0x3F)<<8 | int(msg[off+1])
			sub, _, err := readDNSName(msg, ptr)
			if err != nil {
				return "", 0, err
			}
			if sub != "" {
				labels = append(labels, sub)
			}
			return strings.Join(labels, "."), off + 2, nil
		}
		if off+1+l > len(msg) {
			return "", 0, errors.New("DNS 标签越界")
		}
		labels = append(labels, string(msg[off+1:off+1+l]))
		off += 1 + l
	}
}

// parseDNSAnswers 解析响应中的 A / AAAA / PTR 答案。
func parseDNSAnswers(msg []byte) ([]string, error) {
	if len(msg) < 12 {
		return nil, errors.New("DNS 响应过短")
	}
	rcode := int(msg[3] & 0x0F)
	if rcode != 0 && rcode != 3 { // 3 = NXDOMAIN，按「没有记录」处理
		return nil, fmt.Errorf("DNS 返回 rcode=%d", rcode)
	}
	qd := int(msg[4])<<8 | int(msg[5])
	an := int(msg[6])<<8 | int(msg[7])
	off := 12
	for i := 0; i < qd; i++ {
		_, next, err := readDNSName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next + 4
	}
	var out []string
	for i := 0; i < an; i++ {
		_, next, err := readDNSName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+10 > len(msg) {
			return nil, errors.New("DNS 答案越界")
		}
		rtype := uint16(msg[off])<<8 | uint16(msg[off+1])
		rdlen := int(msg[off+8])<<8 | int(msg[off+9])
		off += 10
		if off+rdlen > len(msg) {
			return nil, errors.New("DNS rdata 越界")
		}
		switch rtype {
		case dnsTypeA:
			if rdlen == net.IPv4len {
				out = append(out, net.IP(msg[off:off+rdlen]).String())
			}
		case dnsTypeAAAA:
			if rdlen == net.IPv6len {
				out = append(out, net.IP(msg[off:off+rdlen]).String())
			}
		case dnsTypePTR:
			name, _, nerr := readDNSName(msg, off)
			if nerr != nil {
				return nil, nerr
			}
			out = append(out, strings.TrimSuffix(name, "."))
		}
		off += rdlen
	}
	return out, nil
}

// parseDNSMXAnswers 解析响应中的 MX 答案（priority + exchange 原始值）。
func parseDNSMXAnswers(msg []byte) ([]mxRecord, error) {
	if len(msg) < 12 {
		return nil, errors.New("DNS 响应过短")
	}
	rcode := int(msg[3] & 0x0F)
	if rcode != 0 && rcode != 3 { // 3 = NXDOMAIN，按「没有记录」处理
		return nil, fmt.Errorf("DNS 返回 rcode=%d", rcode)
	}
	qd := int(msg[4])<<8 | int(msg[5])
	an := int(msg[6])<<8 | int(msg[7])
	off := 12
	for i := 0; i < qd; i++ {
		_, next, err := readDNSName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next + 4
	}
	var out []mxRecord
	for i := 0; i < an; i++ {
		_, next, err := readDNSName(msg, off)
		if err != nil {
			return nil, err
		}
		off = next
		if off+10 > len(msg) {
			return nil, errors.New("DNS 答案越界")
		}
		rtype := uint16(msg[off])<<8 | uint16(msg[off+1])
		rdlen := int(msg[off+8])<<8 | int(msg[off+9])
		off += 10
		if off+rdlen > len(msg) {
			return nil, errors.New("DNS rdata 越界")
		}
		if rtype == dnsTypeMX && rdlen >= 3 {
			pref := int(msg[off])<<8 | int(msg[off+1])
			host, _, nerr := readDNSName(msg, off+2)
			if nerr != nil {
				return nil, nerr
			}
			if host = strings.TrimSuffix(strings.TrimSpace(host), "."); host != "" {
				out = append(out, mxRecord{Preference: pref, Host: host})
			}
		}
		off += rdlen
	}
	return out, nil
}

// dnsUDPExchange 直接向 upstream 发一次 UDP 查询并返回原始响应报文。
func dnsUDPExchange(upstream, name string, qtype uint16) ([]byte, error) {
	query, _, err := buildDNSQuery(name, qtype)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("udp", upstream, mailDNSQueryTimeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(mailDNSQueryTimeout))
	if _, err := conn.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// dnsQueryUDP 直接向 upstream 发一次 UDP 查询（A / AAAA / PTR）。
func dnsQueryUDP(upstream, name string, qtype uint16) ([]string, error) {
	msg, err := dnsUDPExchange(upstream, name, qtype)
	if err != nil {
		return nil, err
	}
	return parseDNSAnswers(msg)
}

type dohAnswer struct {
	Name string `json:"name"`
	Type int    `json:"type"`
	Data string `json:"data"`
}

type dohResponse struct {
	Status int         `json:"Status"`
	Answer []dohAnswer `json:"Answer"`
}

// dohQuery 用 DNS-over-HTTPS（JSON）查一次，返回结构化响应。
func dohQuery(name string, qtype uint16) (dohResponse, error) {
	var dr dohResponse
	q := mailDoHEndpoint + "?name=" + url.QueryEscape(strings.TrimSuffix(strings.TrimSpace(name), ".")) +
		"&type=" + strconv.Itoa(int(qtype))
	ctx, cancel := context.WithTimeout(context.Background(), mailDNSQueryTimeout+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return dr, err
	}
	req.Header.Set("accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return dr, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return dr, fmt.Errorf("DoH 返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return dr, err
	}
	if dr.Status != 0 && dr.Status != 3 {
		return dr, fmt.Errorf("DoH 返回 Status=%d", dr.Status)
	}
	return dr, nil
}

// dnsQueryDoH 用 DNS-over-HTTPS（JSON）查询 A / AAAA / PTR，作为 UDP 53 被拦截时的兜底。
func dnsQueryDoH(name string, qtype uint16) ([]string, error) {
	dr, err := dohQuery(name, qtype)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range dr.Answer {
		if a.Type != int(qtype) {
			continue
		}
		out = append(out, strings.TrimSuffix(a.Data, "."))
	}
	return out, nil
}

// dnsQueryDoHMX 用 DoH 查询 MX，解析成 priority + exchange。
func dnsQueryDoHMX(name string) ([]mxRecord, error) {
	dr, err := dohQuery(name, dnsTypeMX)
	if err != nil {
		return nil, err
	}
	var out []mxRecord
	for _, a := range dr.Answer {
		if a.Type != int(dnsTypeMX) {
			continue
		}
		if rec, ok := parseDoHMXData(a.Data); ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// parseDoHMXData 解析 DoH JSON 里 MX 的 data 字段，形如 "10 mail.example.com."。
func parseDoHMXData(data string) (mxRecord, bool) {
	fields := strings.Fields(strings.TrimSpace(data))
	if len(fields) < 2 {
		return mxRecord{}, false
	}
	pref, err := strconv.Atoi(fields[0])
	if err != nil || pref < 0 {
		return mxRecord{}, false
	}
	host := strings.TrimSuffix(fields[1], ".")
	if host == "" {
		return mxRecord{}, false
	}
	return mxRecord{Preference: pref, Host: host}, true
}

// mailDNSQuery 并发查询：直连公共 DNS 与 DoH 一起发，先成功的结果胜出。
//
// 并发的原因：某些机房会封掉出站 UDP 53，串行重试会让面板「邮件设置」页卡十几秒；
// 并发下最坏也只等一次超时。所有通道都失败时返回第一个错误（状态会是 unknown，
// 而不是误报 missing）。
func mailDNSQuery(name string, qtype uint16) ([]string, error) {
	type dnsResult struct {
		vals []string
		err  error
	}
	total := len(mailDNSUpstreams) + 1
	ch := make(chan dnsResult, total)
	for _, upstream := range mailDNSUpstreams {
		go func(up string) {
			vals, err := dnsQueryUDP(up, name, qtype)
			ch <- dnsResult{vals: vals, err: err}
		}(upstream)
	}
	go func() {
		vals, err := dnsQueryDoH(name, qtype)
		ch <- dnsResult{vals: vals, err: err}
	}()
	var firstErr error
	for i := 0; i < total; i++ {
		r := <-ch
		if r.err == nil {
			return r.vals, nil // 空结果同样算「确实没有这条记录」
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}
	return nil, firstErr
}

// mailDNSQueryMX 并发查询 MX：直连公共 DNS 与 DoH 一起发，先成功的结果胜出。
// 结果按 priority 升序并去重（MX 的优先级是有语义的，必须保留原始值）。
func mailDNSQueryMX(name string) ([]mxRecord, error) {
	type mxResult struct {
		vals []mxRecord
		err  error
	}
	total := len(mailDNSUpstreams) + 1
	ch := make(chan mxResult, total)
	for _, upstream := range mailDNSUpstreams {
		go func(up string) {
			msg, err := dnsUDPExchange(up, name, dnsTypeMX)
			if err != nil {
				ch <- mxResult{err: err}
				return
			}
			vals, perr := parseDNSMXAnswers(msg)
			ch <- mxResult{vals: vals, err: perr}
		}(upstream)
	}
	go func() {
		vals, err := dnsQueryDoHMX(name)
		ch <- mxResult{vals: vals, err: err}
	}()
	var firstErr error
	for i := 0; i < total; i++ {
		r := <-ch
		if r.err == nil {
			return sortMXRecords(r.vals), nil // 空结果同样算「确实没有 MX」
		}
		if firstErr == nil {
			firstErr = r.err
		}
	}
	return nil, firstErr
}

// lookupPTRDirect 用公共 DNS 反查 IP 的 PTR（绕开 /etc/hosts 与本地缓存）。
func lookupPTRDirect(ip string) ([]string, error) {
	name, err := dnsReverseName(ip)
	if err != nil {
		return nil, err
	}
	return mailDNSQuery(name, dnsTypePTR)
}

// lookupHostDirect 用公共 DNS 正查 A（A 为空再查 AAAA），用于 forward-confirm。
func lookupHostDirect(name string) ([]string, error) {
	addrs, err := mailDNSQuery(name, dnsTypeA)
	if err != nil {
		return nil, err
	}
	if len(addrs) > 0 {
		return addrs, nil
	}
	return mailDNSQuery(name, dnsTypeAAAA)
}

// ---- MX 原始值检测 ----

// mxRecord 是一条真实 MX 记录（priority + exchange）。
type mxRecord struct {
	Preference int
	Host       string
}

// mxLookup 是一次 MX 查询结果：Records 为空 + Err==nil 表示「确实没有 MX 记录」。
type mxLookup struct {
	Records []mxRecord
	Err     error
}

// lookupMXDirect 直接向公共 DNS 查询域名的 MX 记录（只信 MX 答案，不做正向解析推断）。
func lookupMXDirect(domain string) ([]mxRecord, error) {
	domain = strings.TrimSuffix(strings.TrimSpace(domain), ".")
	if domain == "" {
		return nil, errors.New("MX 查询域名为空")
	}
	return mailDNSQueryMX(domain)
}

// sortMXRecords 按 priority 升序排序并去掉完全重复的记录。
// MX 的 priority 有语义（数值越小越优先），必须原样保留。
func sortMXRecords(in []mxRecord) []mxRecord {
	out := make([]mxRecord, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		if r.Host == "" {
			continue
		}
		k := strconv.Itoa(r.Preference) + "|" + strings.ToLower(r.Host)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Preference != out[j].Preference {
			return out[i].Preference < out[j].Preference
		}
		return out[i].Host < out[j].Host
	})
	return out
}

// formatMXRecords 把 MX 记录格式化成「优先级 + exchange」，用于展示原始值。
func formatMXRecords(recs []mxRecord) string {
	parts := make([]string, 0, len(recs))
	for _, r := range recs {
		parts = append(parts, strconv.Itoa(r.Preference)+" "+r.Host)
	}
	return strings.Join(parts, "; ")
}

// suggestMXValue 是「域名没有任何 MX 记录」时给出的建议值。
func suggestMXValue(domain string) string {
	return "10 mail." + domain
}

// buildMXRecord 把 MX 查询结果组装成面板展示用的记录。
//
// Status 只看 MX 查询本身：查到记录 = ok；查询成功但为空 = missing；查询失败 = unknown。
// 「是否指向本机」「是否与 PTR 主机名一致」只写进 Detail，不影响 MX 是否存在。
func buildMXRecord(domain, egressIP string, ptr ptrVerdict, mx mxLookup, resolveHost func(string) ([]string, error)) dnsRecord {
	rec := dnsRecord{Type: "MX", Host: domain, TTL: 300, Key: "mx"}
	rec.Note = "MX（收信路由）：这里显示的是直连公共 DNS 查到的真实 MX 记录（优先级 + exchange），" +
		"不是用正向解析推断出来的。自建直投「发信」本身不依赖这条记录，但让 MX 主机与 PTR 主机名一致、" +
		"且该主机 A 记录指向本机，对收件方判断「这台机器是该域的合法邮件主机」最有帮助。"

	switch {
	case mx.Err != nil:
		rec.Status = ptrStatusUnknown
		rec.Detail = domain + " 的 MX 查询失败（公共 DNS 与 DoH 都不可用）：" + mx.Err.Error()
	case len(mx.Records) == 0:
		rec.Status = ptrStatusMissing
		rec.Value = suggestMXValue(domain)
		target := strings.TrimSpace(strings.TrimPrefix(rec.Value, "10 "))
		tip := "先给 " + target + " 加一条 A 记录指向本机公网 IP"
		if ip := strings.TrimSpace(egressIP); ip != "" {
			tip = "先给 " + target + " 加一条 A 记录指向 " + ip
		}
		rec.Detail = domain + " 当前没有 MX 记录（NODATA）。建议值：" + rec.Value + "（" +
			tip + "，再到 VPS 服务商把 PTR 也设成同一个名字）"
	default:
		rec.Status = ptrStatusOK
		rec.Value = formatMXRecords(mx.Records)
		rec.Actual = rec.Value
		rec.Detail = describeMX(mx.Records, egressIP, ptr, resolveHost)
	}
	return rec
}

// describeMX 生成 MX 的附注：是否指向本机、是否与 PTR 主机名一致。
func describeMX(recs []mxRecord, egressIP string, ptr ptrVerdict, resolveHost func(string) ([]string, error)) string {
	hosts := uniqueMXHosts(recs)
	parts := []string{"MX 主机：" + strings.Join(hosts, ", ")}
	if egressIP == "" {
		parts = append(parts, "未探测到本机公网出口 IP，无法判断 MX 是否指向本机")
	} else if resolveHost != nil {
		resolved := resolveMXHosts(hosts, resolveHost)
		var onHost, offHost, failed []string
		for _, h := range hosts {
			r := resolved[h]
			switch {
			case r.Err != nil:
				failed = append(failed, h)
			case containsIP(r.Addrs, egressIP):
				onHost = append(onHost, h)
			default:
				offHost = append(offHost, h)
			}
		}
		if len(onHost) > 0 {
			parts = append(parts, "指向本机（"+egressIP+"）："+strings.Join(onHost, ", "))
		}
		if len(offHost) > 0 {
			parts = append(parts, "不指向本机："+strings.Join(offHost, ", "))
		}
		if len(failed) > 0 {
			parts = append(parts, "正查失败无法判断："+strings.Join(failed, ", "))
		}
	}

	ptrName := firstPTRName(ptr.Actual)
	if ptrName == "" {
		parts = append(parts, "本机 IP 还没有 PTR，无法做 PTR/MX 一致性比对")
	} else if listContainsFold(hosts, ptrName) {
		parts = append(parts, "与 PTR 主机名一致（"+ptrName+"）")
	} else {
		parts = append(parts, "与 PTR 主机名不一致（PTR 为 "+ptrName+"，MX 为 "+strings.Join(hosts, ", ")+"）")
	}
	return strings.Join(parts, "；")
}

// firstPTRName 取原始 PTR 值里的第一个主机名。
func firstPTRName(actual string) string {
	for _, part := range strings.Split(actual, ",") {
		if p := strings.TrimSuffix(strings.TrimSpace(part), "."); p != "" {
			return p
		}
	}
	return ""
}

func uniqueMXHosts(recs []mxRecord) []string {
	var out []string
	seen := map[string]struct{}{}
	for _, r := range recs {
		k := strings.ToLower(r.Host)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, r.Host)
	}
	return out
}

func containsIP(addrs []string, ip string) bool {
	for _, a := range addrs {
		if sameIP(a, ip) {
			return true
		}
	}
	return false
}

func listContainsFold(list []string, s string) bool {
	for _, item := range list {
		if strings.EqualFold(item, s) {
			return true
		}
	}
	return false
}

type mxHostResolution struct {
	Addrs []string
	Err   error
}

// resolveMXHosts 并发正查各 MX 主机的 A/AAAA（只用于判断「是否指向本机」）。
func resolveMXHosts(hosts []string, resolve func(string) ([]string, error)) map[string]mxHostResolution {
	out := make(map[string]mxHostResolution, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, host := range hosts {
		wg.Add(1)
		go func(h string) {
			defer wg.Done()
			addrs, err := resolve(h)
			mu.Lock()
			out[h] = mxHostResolution{Addrs: addrs, Err: err}
			mu.Unlock()
		}(host)
	}
	wg.Wait()
	return out
}

func shortText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// localServerIP4SQL 读取本机（控制服务器）采集到的公网 IPv4。
//
// local_server 表（0040_local_server.sql）的列名是 ip4/ip6/ip，旧代码查的
// local_server.last_ip4 不存在，SQL 报错又被丢弃 → mailEgressIP 的第三级兜底永远失效（审计 P2-4）。
const localServerIP4SQL = `SELECT ip4 FROM local_server WHERE id = 1`

// localServerIP4 返回本机记录的公网 IPv4；读不到返回空串（调用方继续兜底）。
func (h *handlers) localServerIP4(ctx context.Context) string {
	var ip sql.NullString
	if err := h.db.QueryRowContext(ctx, localServerIP4SQL).Scan(&ip); err != nil || !ip.Valid {
		if err != nil && err != sql.ErrNoRows && h.log != nil {
			h.log.Warn("读取本机公网 IP 失败", "err", err)
		}
		return ""
	}
	return strings.TrimSpace(ip.String)
}

// mailEgressIP 返回发信用本机公网出口 IP。
//
// 优先级：设置项 mail_egress_ip → UDP 探测 → 本机服务器记录里的 ip4。
// SPF 必须精确到 IP，写错会导致 softfail 进垃圾箱。
func (h *handlers) mailEgressIP() string {
	if v := strings.TrimSpace(h.settingValue(context.Background(), settingMailEgressIP)); v != "" {
		return v
	}
	if ip := detectEgressIPv4(); ip != "" {
		return ip
	}
	// 回落到本机服务器记录（面板已采集的公网地址）
	return h.localServerIP4(context.Background())
}

// detectEgressIPv4 用 UDP connect 技巧拿本机出口地址（不发任何包）。
func detectEgressIPv4() string {
	conn, err := net.Dial("udp4", "1.1.1.1:80")
	if err != nil {
		return ""
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return ""
	}
	ip := addr.IP.To4()
	if ip == nil {
		return ""
	}
	// 私网地址说明本机在 NAT 后面，这个值不能写进 SPF
	if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ""
	}
	return ip.String()
}
