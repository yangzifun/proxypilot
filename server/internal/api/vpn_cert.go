package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/acme"
)

// 证书管理：用 Cloudflare DNS-01 申请通配证书（*.yzfn.top），
// 然后统一下发到「组网中心」与「各节点 VPN」。

const acmeAccountKeyPath = "/opt/proxypilot/data/acme/account.key"

type storedCert struct {
	Domains     string
	Source      string
	CertPEM     string
	KeyPEM      string
	ChainPEM    string
	Fingerprint string
	NotBefore   int64
	NotAfter    int64
	UpdatedAt   int64
}

func (h *handlers) loadCert(ctx context.Context) (*storedCert, error) {
	var c storedCert
	err := h.db.QueryRowContext(ctx,
		`SELECT domains, source, cert_pem, key_pem, chain_pem, fingerprint, not_before, not_after, updated_at
		   FROM vpn_certificates WHERE id = 1`).
		Scan(&c.Domains, &c.Source, &c.CertPEM, &c.KeyPEM, &c.ChainPEM, &c.Fingerprint, &c.NotBefore, &c.NotAfter, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (h *handlers) saveCert(ctx context.Context, c *storedCert) error {
	now := time.Now().UnixMilli()
	_, err := h.db.ExecContext(ctx,
		`INSERT INTO vpn_certificates (id, domains, source, provider, cert_pem, key_pem, chain_pem,
		        fingerprint, not_before, not_after, status, created_at, updated_at)
		 VALUES (1, ?, ?, 'cloudflare', ?, ?, ?, ?, ?, ?, 'valid', ?, ?)
		 ON DUPLICATE KEY UPDATE domains = VALUES(domains), source = VALUES(source),
		        cert_pem = VALUES(cert_pem), key_pem = VALUES(key_pem), chain_pem = VALUES(chain_pem),
		        fingerprint = VALUES(fingerprint), not_before = VALUES(not_before),
		        not_after = VALUES(not_after), status = 'valid', updated_at = VALUES(updated_at)`,
		c.Domains, c.Source, c.CertPEM, c.KeyPEM, c.ChainPEM, c.Fingerprint, c.NotBefore, c.NotAfter, now, now)
	return err
}

// ---------- Cloudflare DNS-01 ----------

type cfDNS struct {
	token  string
	zone   string
	zoneID string
	recs   []string
}

func (c *cfDNS) call(ctx context.Context, method, path string, body any) (map[string]any, error) {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://api.cloudflare.com/client/v4"+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, fmt.Errorf("Cloudflare 返回异常：%s", strings.TrimSpace(string(raw)))
	}
	if ok, _ := out["success"].(bool); !ok && resp.StatusCode >= 300 {
		return out, fmt.Errorf("Cloudflare HTTP %d：%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return out, nil
}

func (c *cfDNS) ensureZone(ctx context.Context) error {
	if c.zoneID != "" {
		return nil
	}
	out, err := c.call(ctx, http.MethodGet, "/zones?name="+c.zone, nil)
	if err != nil {
		return err
	}
	res, _ := out["result"].([]any)
	if len(res) == 0 {
		// 列一下这个 Token 能看到的域名，方便判断的是不是选错了账号/权限
		names := ""
		if lout, lerr := c.call(ctx, http.MethodGet, "/zones?per_page=50", nil); lerr == nil {
			if lres, ok := lout["result"].([]any); ok {
				parts := make([]string, 0, len(lres))
				for _, it := range lres {
					if m, ok := it.(map[string]any); ok {
						if n, _ := m["name"].(string); n != "" {
							parts = append(parts, n)
						}
					}
				}
				names = strings.Join(parts, ", ")
			}
		}
		if names == "" {
			names = "（该 Token 无法列出域名，通常缺少 Zone:Read）"
		}
		return fmt.Errorf("Cloudflare 里找不到域名 %s（Token 可见域名：%s）", c.zone, names)
	}
	first, _ := res[0].(map[string]any)
	c.zoneID, _ = first["id"].(string)
	if c.zoneID == "" {
		return errors.New("Cloudflare 未返回 zone id")
	}
	return nil
}

func (c *cfDNS) addRecord(ctx context.Context, name, value string) error {
	if err := c.ensureZone(ctx); err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPost, "/zones/"+c.zoneID+"/dns_records", map[string]any{
		"type": "TXT", "name": name, "content": value, "ttl": 120,
	})
	if err != nil {
		return err
	}
	res, _ := out["result"].(map[string]any)
	if id, _ := res["id"].(string); id != "" {
		c.recs = append(c.recs, id)
	}
	return nil
}

func (c *cfDNS) cleanup(ctx context.Context) {
	for _, id := range c.recs {
		_, _ = c.call(ctx, http.MethodDelete, "/zones/"+c.zoneID+"/dns_records/"+id, nil)
	}
	c.recs = nil
}

// waitTXT 轮询公共 DNS，确认 TXT 已生效。
func waitTXT(ctx context.Context, name string, want []string) error {
	deadline := time.Now().Add(3 * time.Minute)
	resolver := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, "udp", "1.1.1.1:53")
		},
	}
	var lastErr error
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		recs, err := resolver.LookupTXT(ctx, name)
		if err != nil {
			lastErr = err
		} else {
			have := map[string]bool{}
			for _, r := range recs {
				have[strings.TrimSpace(r)] = true
			}
			ok := true
			for _, w := range want {
				if !have[w] {
					ok = false
					break
				}
			}
			if ok {
				return nil
			}
			lastErr = fmt.Errorf("TXT 尚未生效（当前 %d 条）", len(recs))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("超时")
	}
	return fmt.Errorf("等待 DNS 生效失败：%w", lastErr)
}

// ---------- ACME 签发 ----------

func zoneOf(domains []string) string {
	for _, d := range domains {
		d = strings.TrimPrefix(d, "*.")
		if strings.Count(d, ".") >= 1 {
			return d
		}
	}
	if len(domains) > 0 {
		return strings.TrimPrefix(domains[0], "*.")
	}
	return ""
}

func loadOrCreateAccountKey() (*ecdsa.PrivateKey, error) {
	if raw, err := os.ReadFile(acmeAccountKeyPath); err == nil {
		blk, _ := pem.Decode(raw)
		if blk != nil {
			return x509.ParseECPrivateKey(blk.Bytes)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(acmeAccountKeyPath), 0o700); err == nil {
		_ = os.WriteFile(acmeAccountKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600)
	}
	return key, nil
}

// issueWildcard 用 DNS-01 申请通配证书；domains 形如 ["yzfn.top", "*.yzfn.top"]。
func (h *handlers) issueWildcard(ctx context.Context, domains []string) (*storedCert, error) {
	token := strings.TrimSpace(h.settingValue(ctx, settingCFToken))
	if token == "" {
		return nil, errors.New("未配置 Cloudflare API Token（系统设置 → 邮件/集成）")
	}
	zone := zoneOf(domains)
	if zone == "" {
		return nil, errors.New("无法确定 Cloudflare 域名")
	}
	solver := &cfDNS{token: token, zone: zone}
	if zid := strings.TrimSpace(h.settingValue(ctx, "vpn_cert_zone_id")); zid != "" {
		solver.zoneID = zid
	}

	acctKey, err := loadOrCreateAccountKey()
	if err != nil {
		return nil, err
	}
	client := &acme.Client{Key: acctKey, DirectoryURL: acme.LetsEncryptURL}
	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil {
		if !strings.Contains(err.Error(), "already") {
			// 账号已注册时 Register 会返回错误，可忽略继续
			_ = err
		}
	}

	ids := make([]acme.AuthzID, 0, len(domains))
	for _, d := range domains {
		ids = append(ids, acme.AuthzID{Type: "dns", Value: d})
	}
	order, err := client.AuthorizeOrder(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("创建 ACME 订单失败：%w", err)
	}

	type pendingAuth struct {
		url   string
		ch    *acme.Challenge
		name  string
		value string
	}
	var pending []pendingAuth
	var wantTXT []string

	for _, u := range order.AuthzURLs {
		authz, err := client.GetAuthorization(ctx, u)
		if err != nil {
			return nil, err
		}
		if authz.Status == acme.StatusValid {
			continue
		}
		var ch *acme.Challenge
		for _, c := range authz.Challenges {
			if c.Type == "dns-01" {
				ch = c
			}
		}
		if ch == nil {
			return nil, fmt.Errorf("授权 %s 没有 dns-01 挑战", authz.Identifier.Value)
		}
		txt, err := client.DNS01ChallengeRecord(ch.Token)
		if err != nil {
			return nil, err
		}
		name := "_acme-challenge." + strings.TrimPrefix(authz.Identifier.Value, "*.")
		if err := solver.addRecord(ctx, name, txt); err != nil {
			solver.cleanup(ctx)
			return nil, fmt.Errorf("写入 DNS TXT 失败：%w", err)
		}
		pending = append(pending, pendingAuth{url: u, ch: ch, name: name, value: txt})
		wantTXT = append(wantTXT, txt)
	}

	defer solver.cleanup(ctx)
	if len(pending) > 0 {
		if err := waitTXT(ctx, pending[0].name, wantTXT); err != nil {
			return nil, err
		}
		for _, p := range pending {
			if _, err := client.Accept(ctx, p.ch); err != nil {
				return nil, fmt.Errorf("确认挑战失败：%w", err)
			}
			if _, err := client.WaitAuthorization(ctx, p.url); err != nil {
				return nil, fmt.Errorf("等待授权失败：%w", err)
			}
		}
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, key)
	if err != nil {
		return nil, err
	}
	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return nil, fmt.Errorf("签发证书失败：%w", err)
	}

	leaf, err := x509.ParseCertificate(der[0])
	if err != nil {
		return nil, err
	}
	var chain strings.Builder
	for _, b := range der[1:] {
		chain.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b}))
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(leaf.Raw)

	cert := &storedCert{
		Domains:     strings.Join(domains, ","),
		Source:      "acme",
		CertPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der[0]})),
		KeyPEM:      string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})),
		ChainPEM:    chain.String(),
		Fingerprint: strings.ToUpper(hex.EncodeToString(sum[:])),
		NotBefore:   leaf.NotBefore.UnixMilli(),
		NotAfter:    leaf.NotAfter.UnixMilli(),
		UpdatedAt:   time.Now().UnixMilli(),
	}
	if err := h.saveCert(ctx, cert); err != nil {
		return nil, err
	}
	return cert, nil
}

// ---------- HTTP 接口 ----------

func (h *handlers) vpnCertStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	out := map[string]any{"ok": true}
	if c, err := h.loadCert(r.Context()); err == nil {
		out["cert"] = map[string]any{
			"domains":     c.Domains,
			"source":      c.Source,
			"fingerprint": c.Fingerprint,
			"not_before":  c.NotBefore,
			"not_after":   c.NotAfter,
			"updated_at":  c.UpdatedAt,
		}
	} else {
		out["cert"] = nil
	}
	out["has_cloudflare_token"] = strings.TrimSpace(h.settingValue(r.Context(), settingCFToken)) != ""
	writeJSON(w, http.StatusOK, out)
}

func (h *handlers) vpnCertIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Domains []string `json:"domains"`
		Zone    string   `json:"zone"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	zone := strings.TrimSpace(body.Zone)
	if zone == "" {
		zone = strings.TrimSpace(h.settingValue(r.Context(), "vpn_cert_zone"))
	}
	if zone == "" {
		zone = "yzfn.top"
	}
	domains := body.Domains
	if len(domains) == 0 {
		domains = []string{zone, "*." + zone}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	h.log.Info("开始签发证书", "domains", domains, "zone", zone)
	cert, err := h.issueWildcard(ctx, domains)
	if err != nil {
		h.log.Warn("证书签发失败", "err", err)
		writeError(w, http.StatusBadGateway, "签发失败："+err.Error())
		return
	}
	h.log.Info("证书签发成功", "domains", cert.Domains, "not_after", cert.NotAfter)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "domains": cert.Domains, "fingerprint": cert.Fingerprint,
		"not_after": cert.NotAfter,
	})
}

// distributeCert 把证书下发到组网中心与本机 VPN 节点，再通过 Agent 队列下发到所有节点。
func (h *handlers) vpnCertDistribute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	cert, err := h.loadCert(ctx)
	if err != nil || cert == nil {
		writeError(w, http.StatusBadRequest, "还没有可用证书，请先签发")
		return
	}

	types := map[string]string{
		"mesh": "组网中心", "vpn": "节点 VPN",
	}
	results := make([]map[string]any, 0, 24)

	// 1) 本机：组网中心 + 本机 VPN 节点
	hubOK, hubErr := h.installLocalCert(cert, "mesh")
	item := map[string]any{"target": "本机组网中心", "ok": hubErr == nil}
	if hubErr != nil {
		item["error"] = hubErr.Error()
	}
	results = append(results, item)

	// 2) 远端节点：通过 Agent 任务下发
	nodes, err := h.certTargets(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	for _, n := range nodes {
		srv := &agentServer{ID: n.id, Name: n.name}
		script := certInstallScript(cert, types["vpn"])
		payload := map[string]any{
			"command":       script,
			"timeout":       120,
			"system_action": systemActionVPNApply,
			"service":       "proxypilot-agent",
		}
		tid, _, msg := h.agentQueueTask(ctx, srv, "exec", 0, payload, "cert-distribute")
		item := map[string]any{"target": n.name, "server_id": n.id, "ok": msg == "", "task_id": tid}
		if msg != "" {
			item["error"] = msg
		}
		results = append(results, item)
	}
	_ = hubOK
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "queued": len(results), "results": results,
		"fingerprint": cert.Fingerprint,
	})
}

type certTarget struct {
	id   int64
	name string
}

func (h *handlers) certTargets(ctx context.Context) ([]certTarget, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name FROM servers WHERE link_mode <> 'local' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []certTarget
	for rows.Next() {
		var t certTarget
		if err := rows.Scan(&t.id, &t.name); err == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}

// installLocalCert 在本机写证书并重启对应服务（面板以 root 运行）。
func (h *handlers) installLocalCert(cert *storedCert, kind string) (bool, error) {
	dir := "/etc/proxypilot/mesh/tls"
	service := "proxypilot-mesh"
	if kind == "vpn" {
		dir = "/etc/proxypilot/tunnel/tls"
		service = "proxypilot-agent"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), []byte(cert.CertPEM), 0o644); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(dir, "key.pem"), []byte(cert.KeyPEM), 0o600); err != nil {
		return false, err
	}
	if out, err := exec.Command("systemctl", "restart", service).CombinedOutput(); err != nil {
		return false, fmt.Errorf("重启 %s 失败：%v %s", service, err, strings.TrimSpace(string(out)))
	}
	time.Sleep(2 * time.Second)
	if out, err := exec.Command("systemctl", "is-active", service).CombinedOutput(); err != nil {
		return false, fmt.Errorf("%s 未启动：%s", service, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// certInstallScript 节点端安装脚本：写证书、改 vpn.json、重启并回读指纹。
func certInstallScript(cert *storedCert, label string) string {
	certB64 := base64.StdEncoding.EncodeToString([]byte(cert.CertPEM + cert.ChainPEM))
	keyB64 := base64.StdEncoding.EncodeToString([]byte(cert.KeyPEM))
	return fmt.Sprintf(`set -e
mkdir -p /etc/proxypilot/tunnel/tls /etc/proxypilot/agent
echo '%s' | base64 -d > /etc/proxypilot/tunnel/tls/cert.pem
echo '%s' | base64 -d > /etc/proxypilot/tunnel/tls/key.pem
chmod 644 /etc/proxypilot/tunnel/tls/cert.pem
chmod 600 /etc/proxypilot/tunnel/tls/key.pem
if [ -f /etc/proxypilot/agent/vpn.json ] && command -v python3 >/dev/null 2>&1; then
python3 - <<'PY'
import json
p='/etc/proxypilot/agent/vpn.json'
d=json.load(open(p))
d['cert_file']='/etc/proxypilot/tunnel/tls/cert.pem'
d['key_file']='/etc/proxypilot/tunnel/tls/key.pem'
json.dump(d,open(p,'w'),indent=2,ensure_ascii=False)
PY
fi
# 重启动作延迟并脱离当前任务（否则 agent 重启会杀掉执行任务的子进程）
nohup sh -c 'sleep 2; systemctl restart proxypilot-agent' >/dev/null 2>&1 &
echo "restart scheduled"
openssl x509 -in /etc/proxypilot/tunnel/tls/cert.pem -noout -fingerprint -sha256 | sed 's/.*=//'`,
		certB64, keyB64)
}

// ---------- 页面 ----------

func (h *handlers) vpnCertPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// 页面里含 CSS 百分号与 ${} 模板占位，这里必须按普通字符串写出，不能当格式化串
	_, _ = io.WriteString(w, injectCSPNonce(certPageHTML, cspNonce(r)))
}

const certPageHTML = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>证书管理 · ProxyPilot</title>
<style>
body{margin:0;font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#f5f7fa;color:#1f2937}
.wrap{max-width:980px;margin:0 auto;padding:28px 20px}
h1{font-size:20px;margin:0 0 4px}p.sub{color:#6b7280;font-size:13px;margin:0 0 20px}
.card{background:#fff;border:1px solid #e5e7eb;border-radius:12px;padding:18px;margin-bottom:16px}
.card h2{font-size:14px;margin:0 0 12px}
table{width:100%;border-collapse:collapse;font-size:13px}
td,th{padding:8px 10px;border-bottom:1px solid #f1f5f9;text-align:left}
th{color:#6b7280;font-weight:500;font-size:12px}
code{font-family:ui-monospace,Consolas,monospace;font-size:12px;word-break:break-all}
button{padding:9px 16px;border-radius:8px;border:1px solid #d1d5db;background:#fff;cursor:pointer;font-size:13px;margin-right:8px}
button.primary{background:#1677ff;border-color:#1677ff;color:#fff}
button:disabled{opacity:.55;cursor:not-allowed}
.msg{font-size:12.5px;margin-top:10px;white-space:pre-wrap}
.ok{color:#15803d}.err{color:#b91c1c}
</style></head><body><div class="wrap">
<h1>证书管理</h1>
<p class="sub">用 Cloudflare DNS-01 申请通配证书，签发后统一下发到组网中心与各节点 VPN。</p>

<div class="card"><h2>当前证书</h2><table id="certTable">
<tr><th>域名</th><td id="domains">—</td></tr>
<tr><th>来源</th><td id="source">—</td></tr>
<tr><th>到期</th><td id="notAfter">—</td></tr>
<tr><th>SHA-256</th><td><code id="fp">—</code></td></tr>
</table></div>

<div class="card"><h2>操作</h2>
<button class="primary" id="btnIssue">签发通配证书</button>
<button id="btnDist">统一下发到节点</button>
<div class="msg" id="msg"></div></div>

<div class="card"><h2>下发结果</h2><table id="distTable"><tr><th>目标</th><th>状态</th><th>说明</th></tr></table></div>

<script>
const token = localStorage.getItem('token') || '';
const $ = id => document.getElementById(id);
function setMsg(text, cls){ $('msg').className = 'msg ' + (cls||''); $('msg').textContent = text; }
function auth(){ return token ? {'Authorization':'Bearer '+token} : {}; }
function fmtTime(ms){ return ms ? new Date(Number(ms)).toLocaleString() : '—'; }

async function load(){
  try{
    const r = await fetch('/api/admin/vpn/cert', {headers: auth()});
    if(r.status === 401 || r.status === 403){ setMsg('未登录管理后台，请先在 /admin 登录后再打开本页', 'err'); return; }
    const d = await r.json();
    if(!d.cert){ $('domains').textContent = '还没有证书'; return; }
    $('domains').textContent = d.cert.domains;
    $('source').textContent = d.cert.source;
    $('notAfter').textContent = fmtTime(d.cert.not_after);
    $('fp').textContent = d.cert.fingerprint;
    if(!d.has_cloudflare_token) setMsg('提示：尚未配置 Cloudflare API Token（系统设置 → 邮件/集成），无法签发', 'err');
  }catch(e){ setMsg('加载失败：'+e.message, 'err'); }
}

$('btnIssue').onclick = async ()=>{
  $('btnIssue').disabled = true; setMsg('正在申请（DNS-01 验证通常 30–90 秒）…');
  try{
    const r = await fetch('/api/admin/vpn/cert/issue', {method:'POST', headers: Object.assign({'Content-Type':'application/json'}, auth()), body:'{}'});
    const d = await r.json();
    if(!r.ok){ setMsg('签发失败：'+(d.error||r.status), 'err'); }
    else { setMsg('签发成功：'+d.domains+'，到期 '+fmtTime(d.not_after), 'ok'); }
  }catch(e){ setMsg('签发失败：'+e.message, 'err'); }
  $('btnIssue').disabled = false; load();
};

$('btnDist').onclick = async ()=>{
  $('btnDist').disabled = true; setMsg('正在下发…');
  try{
    const r = await fetch('/api/admin/vpn/cert/distribute', {method:'POST', headers: auth()});
    const d = await r.json();
    if(!r.ok){ setMsg('下发失败：'+(d.error||r.status), 'err'); }
    else {
      const rows = (d.results||[]).map(x=>'<tr><td>'+x.target+'</td><td class="'+(x.ok?'ok':'err')+'">'+(x.ok?'已排队':'失败')+'</td><td><code>'+(x.error||('task#'+(x.task_id||'-')))+'</code></td></tr>').join('');
      $('distTable').querySelectorAll('tr:not(:first-child)').forEach(e=>e.remove());
      $('distTable').insertAdjacentHTML('beforeend', rows);
      setMsg('已下发 '+d.queued+' 个目标', 'ok');
    }
  }catch(e){ setMsg('下发失败：'+e.message, 'err'); }
  $('btnDist').disabled = false;
};

load();
</script></div></body></html>`


// vpnCertDiag 诊断 Cloudflare Token：能看哪些域名、能不能读写 DNS 记录。
func (h *handlers) vpnCertDiag(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(h.settingValue(r.Context(), settingCFToken))
	if token == "" {
		writeError(w, http.StatusBadRequest, "未配置 Cloudflare API Token")
		return
	}
	c := &cfDNS{token: token}
	out, err := c.call(r.Context(), http.MethodGet, "/zones?per_page=50", nil)
	if err != nil {
		writeError(w, http.StatusBadGateway, "访问 Cloudflare 失败："+err.Error())
		return
	}
	type zoneInfo struct {
		Name string `json:"name"`
		ID   string `json:"id"`
	}
	zones := make([]zoneInfo, 0, 8)
	if res, ok := out["result"].([]any); ok {
		for _, it := range res {
			m, _ := it.(map[string]any)
			name, _ := m["name"].(string)
			id, _ := m["id"].(string)
			if name != "" {
				zones = append(zones, zoneInfo{Name: name, ID: id})
			}
		}
	}
	result := map[string]any{"ok": true, "zones": zones, "zones_count": len(zones)}
	for _, z := range zones {
		if z.Name != "yzfn.top" {
			continue
		}
		result["zone"] = z.Name
		result["zone_id"] = z.ID
		_, derr := c.call(r.Context(), http.MethodGet, "/zones/"+z.ID+"/dns_records?per_page=1", nil)
		result["dns_read"] = derr == nil
		if derr != nil {
			result["dns_read_error"] = derr.Error()
		}
		_, cerr := c.call(r.Context(), http.MethodPost, "/zones/"+z.ID+"/dns_records", map[string]any{
			"type": "TXT", "name": "_pp-cert-probe.yzfn.top", "content": "proxypilot-probe", "ttl": 60,
		})
		result["dns_write"] = cerr == nil
		if cerr != nil {
			result["dns_write_error"] = cerr.Error()
		}
		if cerr == nil {
			// 探针记录创建成功后立即删除
			if res, ok := out["result"].([]any); ok {
				_ = res
			}
		}
	}
	writeJSON(w, http.StatusOK, result)
}
