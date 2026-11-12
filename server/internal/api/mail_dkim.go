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
	"encoding/pem"
	"fmt"
	"net"
	"regexp"
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
	Status string `json:"status"`  // ok / missing / unknown
	Actual string `json:"actual"`  // 实际查到的值（便于排错）
	Key    string `json:"key"`     // 记录标识，便于前端定位
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
	ptrOK, ptrValue := h.checkPTR()
	recs = append(recs, dnsRecord{
		Type: "PTR", Host: "（在 VPS 服务商控制台设置）", TTL: 0, Key: "ptr",
		Value: "面板服务器公网 IP → " + domain,
		Note:  "反向解析。没有 PTR 的 IP 会被 Gmail/QQ 直接判垃圾。注意：PTR 指向的域名最好再配一条 A 记录指回该 IP。",
		Status: map[bool]string{true: "ok", false: "missing"}[ptrOK],
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

// checkPTR 检查本机公网 IP 的反向解析。
func (h *handlers) checkPTR() (bool, string) {
	ip := h.mailEgressIP()
	if ip == "" {
		return false, "未能探测公网出口 IP"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, ip)
	if err != nil || len(names) == 0 {
		return false, ip + " 无 PTR"
	}
	ptr := strings.TrimSuffix(names[0], ".")
	// 服务商默认的 PTR（如 racknerd-b3c2b64）不含点，收件方多半不认
	if !strings.Contains(ptr, ".") {
		return false, ip + " → " + ptr + "（服务商默认值，建议改成自己的域名）"
	}
	return true, ip + " → " + ptr
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
