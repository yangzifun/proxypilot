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
	"strconv"
	"strings"
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
	Status string `json:"status"` // ok / missing / provider_default / forward_mismatch / unknown
	Actual string `json:"actual"` // 实际查到的值（便于排错）
	Key    string `json:"key"`    // 记录标识，便于前端定位
}

// mailDNSGuide 生成发信所需的 DNS 记录指引，并实时校验。
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

	// 实时校验
	for i := range recs {
		status, actual := h.checkTXT(recs[i].Host, recs[i].Key)
		recs[i].Status = status
		recs[i].Actual = actual
	}

	// PTR 单独给一条说明（只能在 VPS 控制台改，不能加 DNS 记录）
	ptrStatus, ptrValue := h.checkPTR()
	recs = append(recs, dnsRecord{
		Type: "PTR", Host: "（在 VPS 服务商控制台设置）", TTL: 0, Key: "ptr",
		Value:  "面板服务器公网 IP → " + domain,
		Note:   "反向解析。没有 PTR 的 IP 会被 Gmail/QQ 直接判垃圾；PTR 指向的域名还必须能正查回本机 IP（forward-confirmed，建议直接用发信域的 MX 主机名）。",
		Status: ptrStatus,
		Actual: ptrValue,
	})
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

// ---- PTR / forward-confirmed 检测 ----
//
// 判定状态（前端按状态显示不同标签）：
//
//	ok               PTR 存在，且正查能回到本机 IP（forward-confirmed 通过）
//	provider_default PTR 只是服务商默认主机名（不含域名后缀，如 racknerd-b3c2b64）
//	forward_mismatch PTR 是域名，但该名字正查不指回本机 IP（常见于域名被 CDN 代理）
//	missing          完全没有 PTR
//	unknown          探测不到出口 IP，或所有 DNS 通道都不可用
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
	ptrStatusForwardMismatch = "forward_mismatch"
	ptrStatusMissing         = "missing"
	ptrStatusUnknown         = "unknown"
)

const (
	dnsTypeA    uint16 = 1
	dnsTypePTR  uint16 = 12
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

// 以下两个变量便于测试注入。
var (
	mailPTRLookup        = lookupPTRDirect
	mailPTRForwardLookup = lookupHostDirect
)

// checkPTR 检查本机公网 IP 的反向解析，并区分「无 PTR / 服务商默认名 / 反查不一致」。
// 返回 (状态, 说明)；状态值见 ptrStatus* 常量。
func (h *handlers) checkPTR() (string, string) {
	ip := h.mailEgressIP()
	if ip == "" {
		return ptrStatusUnknown, "未能探测公网出口 IP"
	}
	names, err := mailPTRLookup(ip)
	return classifyPTR(ip, names, err, mailPTRForwardLookup)
}

// classifyPTR 根据反查（names）与正查（forwardLookup）结果给出状态与说明。
// 抽成纯函数，便于单元测试。
func classifyPTR(ip string, names []string, lookupErr error, forwardLookup func(string) ([]string, error)) (string, string) {
	if lookupErr != nil {
		return ptrStatusUnknown, ip + " PTR 查询失败：" + lookupErr.Error()
	}
	ptr := ""
	for _, n := range names {
		if n = strings.TrimSuffix(strings.TrimSpace(n), "."); n != "" {
			ptr = n
			break
		}
	}
	if ptr == "" {
		return ptrStatusMissing, ip + " 无 PTR（公共 DNS 反查为空）"
	}
	// 服务商默认主机名（如 racknerd-b3c2b64）不带域名后缀，收件方基本不认。
	if !strings.Contains(ptr, ".") {
		return ptrStatusProviderDefault, ip + " → " + ptr + "（服务商默认主机名，不含域名后缀，建议改成发信域名）"
	}
	addrs, ferr := forwardLookup(ptr)
	if ferr != nil {
		return ptrStatusForwardMismatch, ip + " → " + ptr + "（正查失败，无法 forward-confirm：" + ferr.Error() + "）"
	}
	for _, a := range addrs {
		if sameIP(a, ip) {
			return ptrStatusOK, ip + " → " + ptr
		}
	}
	if len(addrs) == 0 {
		return ptrStatusForwardMismatch, ip + " → " + ptr + "（该名字没有 A/AAAA 记录，forward-confirmed 不成立）"
	}
	return ptrStatusForwardMismatch, ip + " → " + ptr + "（正查为 " + strings.Join(addrs, ", ") + "，不含本机 IP，forward-confirmed 不成立）"
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

// dnsQueryUDP 直接向 upstream 发一次 UDP 查询。
func dnsQueryUDP(upstream, name string, qtype uint16) ([]string, error) {
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
	return parseDNSAnswers(buf[:n])
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

// dnsQueryDoH 用 DNS-over-HTTPS（JSON）查询，作为 UDP 53 被拦截时的兜底。
func dnsQueryDoH(name string, qtype uint16) ([]string, error) {
	q := mailDoHEndpoint + "?name=" + url.QueryEscape(strings.TrimSuffix(strings.TrimSpace(name), ".")) +
		"&type=" + strconv.Itoa(int(qtype))
	ctx, cancel := context.WithTimeout(context.Background(), mailDNSQueryTimeout+2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH 返回 HTTP %d", resp.StatusCode)
	}
	var dr dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, err
	}
	if dr.Status != 0 && dr.Status != 3 {
		return nil, fmt.Errorf("DoH 返回 Status=%d", dr.Status)
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
