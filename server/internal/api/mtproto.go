package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	mtprotoDefaultPort    = 13709
	mtprotoDefaultAPIPort = 13719
	mtprotoSecretPrefix   = "ee"
)

var mtprotoPreferIPs = []string{"prefer-ipv4", "prefer-ipv6", "only-ipv4", "only-ipv6"}

var (
	domainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)
	ipv4Re   = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,3}){3}$`)
	secretRe = regexp.MustCompile(`^ee[0-9a-f]+$`)
)

func mtprotoDomainOf(raw string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	if d == "" || len(d) > 253 || !domainRe.MatchString(d) {
		return ""
	}
	if ipv4Re.MatchString(d) {
		return ""
	}
	return d
}

func mtprotoIsIPv4(v string) string {
	s := strings.TrimSpace(v)
	if !ipv4Re.MatchString(s) {
		return ""
	}
	for _, part := range strings.Split(s, ".") {
		n, _ := strconv.Atoi(part)
		if n > 255 {
			return ""
		}
	}
	return s
}

func mtprotoHexOfText(text string) string {
	return hex.EncodeToString([]byte(text))
}

func mtprotoHexToText(h string) string {
	if h == "" || len(h)%2 != 0 || !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(h) {
		return ""
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	return string(b)
}

func mtprotoRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func mtprotoGenerateSecret(domain string) string {
	d := mtprotoDomainOf(domain)
	if d == "" {
		return ""
	}
	return mtprotoSecretPrefix + mtprotoRandomHex(16) + mtprotoHexOfText(d)
}

func mtprotoSecretDomain(secret string) string {
	s := strings.ToLower(strings.TrimSpace(secret))
	if !strings.HasPrefix(s, mtprotoSecretPrefix) {
		return ""
	}
	body := s[len(mtprotoSecretPrefix):]
	if len(body) <= 32 {
		return ""
	}
	return mtprotoHexToText(body[32:])
}

func mtprotoSecretValid(secret string) bool {
	s := strings.ToLower(strings.TrimSpace(secret))
	if !secretRe.MatchString(s) {
		return false
	}
	body := s[2:]
	if len(body) < 34 || len(body)%2 != 0 {
		return false
	}
	return mtprotoDomainOf(mtprotoHexToText(body[32:])) != ""
}

func mtprotoMaskSecret(secret string) string {
	if secret == "" {
		return ""
	}
	if len(secret) <= 14 {
		return "****"
	}
	return secret[:8] + "****" + secret[len(secret)-4:]
}

func mtprotoTomlString(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, v)
	return `"` + r.Replace(cleaned) + `"`
}

func mtprotoHostOf(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if ip := mtprotoIsIPv4(v); ip != "" {
		return ip
	}
	return mtprotoDomainOf(v)
}

type mtprotoTemplate struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Domain      string `json:"domain"`
	Port        int64  `json:"port"`
	APIPort     int64  `json:"api_port"`
	SecretMode  string `json:"secret_mode"`
	Concurrency int64  `json:"concurrency"`
	PreferIP    string `json:"prefer_ip"`
	PublicIPv4  string `json:"public_ipv4"`
	Blocklist   int64  `json:"blocklist"`
	ThrottleMax int64  `json:"throttle_max"`
	ConfigPath  string `json:"config_path"`
	ServiceName string `json:"service_name"`
	Enabled     int64  `json:"enabled"`
}

type mtprotoNode struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	TemplateID      int64  `json:"template_id"`
	Domain          string `json:"domain"`
	Host            string `json:"host"`
	Port            int64  `json:"port"`
	APIPort         int64  `json:"api_port"`
	SecretMode      string `json:"secret_mode"`
	Concurrency     int64  `json:"concurrency"`
	PreferIP        string `json:"prefer_ip"`
	Blocklist       *int64 `json:"blocklist"`
	ThrottleMax     *int64 `json:"throttle_max"`
	ConfigPath      string `json:"config_path"`
	ServiceName     string `json:"service_name"`
	BootstrapSecret string `json:"bootstrap_secret"`
	PublicIPv4      string `json:"public_ipv4"`
	Enabled         *int64 `json:"enabled"`
}

type mtprotoEffectiveConfig struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	Domain          string `json:"domain"`
	Host            string `json:"host"`
	Port            int64  `json:"port"`
	APIPort         int64  `json:"api_port"`
	SecretMode      string `json:"secret_mode"`
	Concurrency     int64  `json:"concurrency"`
	PreferIP        string `json:"prefer_ip"`
	PublicIPv4      string `json:"public_ipv4"`
	Blocklist       int64  `json:"blocklist"`
	ThrottleMax     int64  `json:"throttle_max"`
	ConfigPath      string `json:"config_path"`
	ServiceName     string `json:"service_name"`
	BootstrapSecret string `json:"bootstrap_secret"`
	TemplateID      int64  `json:"template_id"`
	TemplateName    string `json:"template_name"`
	Enabled         int64  `json:"enabled"`
}

type mtprotoClient struct {
	ID     int64  `json:"id"`
	UUID   string `json:"uuid"`
	Email  string `json:"email"`
	Label  string `json:"label"`
	Secret string `json:"secret"`
	Status string `json:"status"`
}

func mtprotoEffective(tpl *mtprotoTemplate, node *mtprotoNode) mtprotoEffectiveConfig {
	pick := func(nv, tv, dflt string) string {
		if strings.TrimSpace(nv) != "" {
			return nv
		}
		if strings.TrimSpace(tv) != "" {
			return tv
		}
		return dflt
	}
	port := node.Port
	if port == 0 {
		port = tpl.Port
	}
	if port == 0 {
		port = mtprotoDefaultPort
	}
	apiPort := node.APIPort
	if apiPort == 0 {
		apiPort = tpl.APIPort
	}
	if apiPort == 0 {
		apiPort = mtprotoDefaultAPIPort
	}
	mode := "multi"
	if strings.ToLower(pick(node.SecretMode, tpl.SecretMode, "multi")) == "single" {
		mode = "single"
	}
	concurrency := node.Concurrency
	if concurrency <= 0 || concurrency > 500000 {
		concurrency = tpl.Concurrency
	}
	if concurrency <= 0 {
		concurrency = 8192
	}
	prefer := pick(node.PreferIP, tpl.PreferIP, "prefer-ipv4")
	if !containsString(mtprotoPreferIPs, prefer) {
		prefer = "prefer-ipv4"
	}
	blocklist := int64(0)
	if node.Blocklist != nil && (*node.Blocklist == 0 || *node.Blocklist == 1) {
		blocklist = *node.Blocklist
	} else if tpl.Blocklist == 1 {
		blocklist = 1
	}
	throttle := tpl.ThrottleMax
	if node.ThrottleMax != nil && *node.ThrottleMax >= 0 {
		throttle = *node.ThrottleMax
	}
	if throttle < 0 {
		throttle = 0
	}
	if throttle > 500000 {
		throttle = 500000
	}
	publicIPv4 := mtprotoIsIPv4(node.Host)
	if publicIPv4 == "" {
		publicIPv4 = mtprotoIsIPv4(pick(node.PublicIPv4, tpl.PublicIPv4, ""))
	}
	return mtprotoEffectiveConfig{
		ID: node.ID, Name: firstNonEmpty(node.Name, "node"),
		Domain: mtprotoDomainOf(pick(node.Domain, tpl.Domain, "")),
		Host:   mtprotoHostOf(node.Host),
		Port:   port, APIPort: apiPort, SecretMode: mode, Concurrency: concurrency,
		PreferIP: prefer, PublicIPv4: publicIPv4, Blocklist: blocklist, ThrottleMax: throttle,
		ConfigPath:      firstNonEmpty(pick(node.ConfigPath, tpl.ConfigPath, ""), "/etc/mtg/config.toml"),
		ServiceName:     firstNonEmpty(pick(node.ServiceName, tpl.ServiceName, ""), "mtg"),
		BootstrapSecret: strings.TrimSpace(node.BootstrapSecret),
		TemplateID:      node.TemplateID, TemplateName: tpl.Name,
		Enabled: func() int64 {
			if node.Enabled != nil {
				return *node.Enabled
			}
			return 1
		}(),
	}
}

func mtprotoActiveClients(rows []mtprotoClient) []mtprotoClient {
	out := []mtprotoClient{}
	for _, c := range rows {
		status := c.Status
		if status == "" {
			status = "active"
		}
		if status == "active" && mtprotoSecretValid(c.Secret) {
			out = append(out, c)
		}
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].ID < out[i].ID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func mtprotoRenderConfig(eff *mtprotoEffectiveConfig, clients []mtprotoClient) (string, map[string]int64, string, string) {
	domain := mtprotoDomainOf(eff.Domain)
	if domain == "" {
		return "", nil, "", "伪装域名无效：请在节点上填写形如 storage.googleapis.com 的域名"
	}
	mode := "multi"
	if strings.EqualFold(eff.SecretMode, "single") {
		mode = "single"
	}
	port := intOrJS(eff.Port, mtprotoDefaultPort, 1, 65535)
	apiPort := intOrJS(eff.APIPort, mtprotoDefaultAPIPort, 1, 65535)
	if apiPort == port {
		return "", nil, "", "统计端口不能与代理端口相同"
	}
	list := mtprotoActiveClients(clients)
	bootstrap := strings.TrimSpace(eff.BootstrapSecret)
	if mode == "single" {
		if !mtprotoSecretValid(bootstrap) {
			return "", nil, "", "共享模式的节点缺少有效 bootstrap_secret，请重新生成"
		}
	} else if len(list) == 0 && !mtprotoSecretValid(bootstrap) {
		return "", nil, "", "节点没有可用的占位 secret，请重新生成 bootstrap_secret"
	}
	prefer := eff.PreferIP
	if !containsString(mtprotoPreferIPs, prefer) {
		prefer = "prefer-ipv4"
	}
	concurrency := intOrJS(eff.Concurrency, 8192, 1, 500000)
	throttle := intOrJS(eff.ThrottleMax, 0, 0, 500000)
	publicIPv4 := mtprotoIsIPv4(eff.Host)
	if publicIPv4 == "" {
		publicIPv4 = mtprotoIsIPv4(eff.PublicIPv4)
	}
	blocklist := eff.Blocklist == 1

	lines := []string{
		"# /etc/mtg/config.toml — 由 ProxyPilot 渲染（V13.4），请勿手改",
		fmt.Sprintf("# node: %s · mode: %s · clients: %d", firstNonEmpty(eff.Name, "node"), mode, func() int {
			if mode == "single" {
				return 1
			}
			return len(list)
		}()),
		"",
	}
	if mode == "single" {
		lines = append(lines, "secret = "+mtprotoTomlString(bootstrap))
	}
	lines = append(lines,
		"bind-to = "+mtprotoTomlString(fmt.Sprintf("[::]:%d", port)),
		"api-bind-to = "+mtprotoTomlString(fmt.Sprintf("127.0.0.1:%d", apiPort)),
		"prefer-ip = "+mtprotoTomlString(prefer),
		fmt.Sprintf("concurrency = %d", concurrency),
	)
	if publicIPv4 != "" {
		lines = append(lines, "public-ipv4 = "+mtprotoTomlString(publicIPv4))
	}
	lines = append(lines, "", "[domain-fronting]", "port = 443", "", "[defense.blocklist]",
		fmt.Sprintf("enabled = %t", blocklist))
	if blocklist {
		lines = append(lines, `urls = ["https://iplists.firehol.org/files/firehol_abuse_1d.netset"]`)
	}
	if throttle > 0 {
		lines = append(lines, "", "[throttle]", fmt.Sprintf("max-connections = %d", throttle))
	}
	if mode == "multi" {
		lines = append(lines, "", "# [secrets] 必须是文件的最后一个 section（TOML 语义：section 之后的键都归属该表）", "[secrets]")
		if len(list) == 0 {
			lines = append(lines, "c0 = "+mtprotoTomlString(bootstrap))
		}
		for _, c := range list {
			lines = append(lines, fmt.Sprintf("c%d = %s", c.ID, mtprotoTomlString(c.Secret)))
		}
	}
	return strings.Join(lines, "\n") + "\n", map[string]int64{"mtproto": port, "api": apiPort}, mode, ""
}

type mtprotoServer struct {
	LastIP4 string `json:"last_ip4"`
	LastIP  string `json:"last_ip"`
	Host    string `json:"host"`
}

func mtprotoLinkHost(eff *mtprotoEffectiveConfig, srv mtprotoServer) string {
	if custom := strings.TrimSpace(eff.Host); custom != "" {
		return custom
	}
	ip := strings.TrimSpace(firstNonEmpty(srv.LastIP4, srv.LastIP))
	if ip != "" && ip != "unknown" {
		return ip
	}
	return strings.TrimSpace(srv.Host)
}

func mtprotoClientLink(eff *mtprotoEffectiveConfig, srv mtprotoServer, client *mtprotoClient) map[string]any {
	host := mtprotoLinkHost(eff, srv)
	port := intOrJS(eff.Port, mtprotoDefaultPort, 1, 65535)
	secret := ""
	if strings.EqualFold(eff.SecretMode, "single") {
		secret = strings.TrimSpace(eff.BootstrapSecret)
	} else if client != nil {
		secret = strings.TrimSpace(client.Secret)
	}
	qs := "server=" + url.QueryEscape(host) + "&port=" + strconv.FormatInt(port, 10) + "&secret=" + url.QueryEscape(secret)
	return map[string]any{
		"host": host, "port": port, "secret": secret,
		"tg":  "tg://proxy?" + qs,
		"tme": "https://t.me/proxy?" + qs,
	}
}
