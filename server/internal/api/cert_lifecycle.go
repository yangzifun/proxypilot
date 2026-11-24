package api

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	certRetryCooldownMs = int64(30 * 60 * 1000)
	certRetryGateMs     = int64(10 * 60 * 1000)
	certRenewWindowMs   = int64(30 * 24 * 3600 * 1000)
	certRenewGateMs     = int64(24 * 3600 * 1000)
)

var (
	certRenewMu    sync.Mutex
	certRenewIssue = func(h *handlers, ctx context.Context, domains []string) (*storedCert, error) {
		return h.issueWildcard(ctx, domains)
	}
)

type certTaskOutput struct {
	Fingerprint string
	SHA256      string
	Service     string
}

func looksLikeCertFingerprint(s string) bool {
	raw := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(s), ":", ""), " ", "")
	if len(raw) < 32 {
		return false
	}
	for _, r := range raw {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// parseCertTaskOutput 解析远端证书脚本输出中的 SHA-256 / 指纹 / sing-box 服务状态。
// 指纹行是 openssl 输出的最后一行（兼容冒号分隔与无分隔）。
func parseCertTaskOutput(output string) certTaskOutput {
	out := certTaskOutput{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "singbox_sha256="):
			out.SHA256 = truncateRunes(strings.TrimSpace(strings.TrimPrefix(line, "singbox_sha256=")), 128)
		case strings.HasPrefix(line, "singbox_service="):
			out.Service = truncateRunes(strings.TrimSpace(strings.TrimPrefix(line, "singbox_service=")), 32)
		case line == "" || strings.Contains(line, "restart scheduled"):
			continue
		case strings.HasPrefix(line, "singbox_"):
			continue
		case looksLikeCertFingerprint(line):
			out.Fingerprint = line
		}
	}
	return out
}

func normalizeCertFingerprint(raw string) string {
	raw = strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(raw), ":", ""), " ", "")
	return strings.ToUpper(raw)
}

// recordCertTaskResult 落库远端证书任务回执；成功写 synced，失败写 failed/error。
func (h *handlers) recordCertTaskResult(ctx context.Context, serverID int64, ok bool, output string) {
	if h.db == nil || serverID <= 0 {
		return
	}
	parsed := parseCertTaskOutput(output)
	status := "failed"
	fingerprint := ""
	errText := truncateRunes(strings.TrimSpace(output), 500)
	if ok {
		fingerprint = parsed.Fingerprint
		if fingerprint == "" && parsed.SHA256 != "" {
			fingerprint = parsed.SHA256
		}
		if fingerprint != "" {
			status = "synced"
			errText = ""
		} else {
			errText = "证书任务回执缺少指纹/SHA-256"
		}
	}
	if fingerprint != "" {
		fingerprint = normalizeCertFingerprint(fingerprint)
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE servers SET cert_status = ?,
		        cert_fingerprint = COALESCE(NULLIF(?, ''), cert_fingerprint),
		        cert_synced_at = ?, cert_error = ?
		  WHERE id = ?`,
		status, fingerprint, nowMillis(), errText, serverID)
}

// updateCertAttempt 记录一次下发尝试（pending/failed），synced_at 同时作为重试冷却基准。
func (h *handlers) updateCertAttempt(ctx context.Context, serverID int64, status, errText string) {
	if h.db == nil || serverID <= 0 {
		return
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE servers SET cert_status = ?, cert_error = ?, cert_synced_at = ? WHERE id = ?`,
		status, truncateRunes(errText, 500), nowMillis(), serverID)
}

func (h *handlers) localCertServerID(ctx context.Context) int64 {
	if h.db == nil {
		return 0
	}
	var id int64
	_ = h.db.QueryRowContext(ctx, `SELECT id FROM servers WHERE link_mode = 'local' ORDER BY id LIMIT 1`).Scan(&id)
	return id
}

// recordLocalCertResult 本机 VPN/sing-box 证书写入结果也落 servers（lib 本机行不出现在 nodes 列表）。
func (h *handlers) recordLocalCertResult(ctx context.Context, cert *storedCert, ok bool, errText string) {
	id := h.localCertServerID(ctx)
	if id <= 0 {
		return
	}
	if ok && cert != nil {
		_, _ = h.db.ExecContext(ctx,
			`UPDATE servers SET cert_status = 'synced', cert_fingerprint = ?, cert_synced_at = ?, cert_error = ''
			  WHERE id = ?`, normalizeCertFingerprint(cert.Fingerprint), nowMillis(), id)
		return
	}
	h.updateCertAttempt(ctx, id, "failed", errText)
}

// queueCertInstall 统一构造证书下发任务（distribute / retry / renew 共用）。
func (h *handlers) queueCertInstall(ctx context.Context, cert *storedCert, srv *agentServer) (int64, string) {
	if cert == nil || srv == nil {
		return 0, "证书/节点为空"
	}
	script := certInstallScript(cert, "节点 VPN")
	payload := map[string]any{
		"command":       script,
		"timeout":       120,
		"system_action": systemActionVPNApply,
		"service":       "proxypilot-agent",
	}
	tid, _, msg := h.agentQueueTask(ctx, srv, "exec", 0, payload, "cert-distribute")
	if msg == "" {
		h.updateCertAttempt(ctx, srv.ID, "pending", "")
	} else {
		h.updateCertAttempt(ctx, srv.ID, "failed", msg)
	}
	return tid, msg
}

// certRetryEligible 判断某节点是否满足离线重试条件。
func certRetryEligible(online bool, status string, syncedAt, now int64) bool {
	if !online {
		return false
	}
	switch status {
	case "", "failed", "pending":
	default:
		return false
	}
	return syncedAt <= 0 || now-syncedAt >= certRetryCooldownMs
}

// certRetryEligibleForCert 额外允许「已同步但证书已过期」的节点重试。
func certRetryEligibleForCert(online bool, status string, syncedAt, now, certNotAfter int64) bool {
	if status == "synced" {
		if certNotAfter <= 0 || certNotAfter > now {
			return false
		}
		if !online {
			return false
		}
		return syncedAt <= 0 || now-syncedAt >= certRetryCooldownMs
	}
	return certRetryEligible(online, status, syncedAt, now)
}

func certNeedsRenew(notAfter, now int64) bool {
	return notAfter > 0 && notAfter-now < certRenewWindowMs
}

// cronCertRetry 每 10 分钟重试空/失败/pending 且在线节点的证书记录。
func (h *handlers) cronCertRetry(ctx context.Context, now int64) {
	last := int64(intOrJS(h.getSetting(ctx, "vpn_cert_retry_at", "0"), 0, 0, 1<<60))
	if now-last < certRetryGateMs {
		return
	}
	_ = h.putSetting(ctx, "vpn_cert_retry_at", itoa64(now))
	cert, err := h.loadCert(ctx)
	if err != nil || cert == nil {
		return
	}
	statuses := "('', 'failed', 'pending')"
	if cert.NotAfter > 0 && cert.NotAfter <= now {
		statuses = "('', 'failed', 'pending', 'synced')"
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, name, COALESCE(cert_status,''), COALESCE(cert_synced_at,0)
		   FROM servers
		  WHERE enabled = 1 AND link_mode <> 'local'
		    AND COALESCE(last_status,'') = 'online'
		    AND COALESCE(cert_status,'') IN `+statuses+`
		    AND (COALESCE(cert_synced_at,0) = 0 OR COALESCE(cert_synced_at,0) < ?)
		  ORDER BY id LIMIT 50`, now-certRetryCooldownMs)
	if err != nil {
		return
	}
	defer rows.Close()
	type target struct {
		id       int64
		name     string
		status   string
		syncedAt int64
	}
	targets := []target{}
	for rows.Next() {
		var t target
		if rows.Scan(&t.id, &t.name, &t.status, &t.syncedAt) == nil {
			if certRetryEligibleForCert(true, t.status, t.syncedAt, now, cert.NotAfter) {
				targets = append(targets, t)
			}
		}
	}
	for _, t := range targets {
		_, msg := h.queueCertInstall(ctx, cert, &agentServer{ID: t.id, Name: t.name})
		if msg != "" && h.log != nil {
			h.log.Warn("证书离线重试入队失败", "server", t.name, "err", msg)
		}
	}
}

// broadcastCert 全量下发：本地 mesh/tunnel/sing-box + 所有远端节点。
// 自动续期成功后使用；结果只记录日志，不阻塞 cron。
func (h *handlers) broadcastCert(ctx context.Context, cert *storedCert) {
	if cert == nil {
		return
	}
	if _, err := h.installLocalCert(cert, "mesh"); err != nil && h.log != nil {
		h.log.Warn("证书续期后本机组网中心下发失败", "err", err)
	}
	if _, err := h.installLocalCert(cert, "vpn"); err != nil {
		if h.log != nil {
			h.log.Warn("证书续期后本机 sing-box 下发失败", "err", err)
		}
		h.recordLocalCertResult(ctx, cert, false, err.Error())
	} else {
		h.recordLocalCertResult(ctx, cert, true, "")
	}
	targets, err := h.certTargets(ctx)
	if err != nil {
		if h.log != nil {
			h.log.Warn("证书续期后查询节点失败", "err", err)
		}
		return
	}
	for _, t := range targets {
		if _, msg := h.queueCertInstall(ctx, cert, &agentServer{ID: t.id, Name: t.name}); msg != "" && h.log != nil {
			h.log.Warn("证书续期后节点下发入队失败", "server", t.name, "err", msg)
		}
	}
}

// cronCertRenew 每日检查到期时间，剩余 <30 天自动续期并全量下发。
func (h *handlers) cronCertRenew(ctx context.Context, now int64) {
	lastCheck := int64(intOrJS(h.getSetting(ctx, "vpn_cert_renew_check_at", "0"), 0, 0, 1<<60))
	if now-lastCheck < certRenewGateMs {
		return
	}
	if !certRenewMu.TryLock() {
		return
	}
	defer certRenewMu.Unlock()
	_ = h.putSetting(ctx, "vpn_cert_renew_check_at", itoa64(now))
	h.invalidateSettings()

	cert, err := h.loadCert(ctx)
	if err != nil || cert == nil || !certNeedsRenew(cert.NotAfter, now) {
		return
	}

	lastRenew := int64(intOrJS(h.getSetting(ctx, "vpn_cert_last_renew_at", "0"), 0, 0, 1<<60))
	if lastRenew > 0 && now-lastRenew < certRenewGateMs {
		return
	}
	domains := []string{}
	for _, d := range strings.Split(cert.Domains, ",") {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, d)
		}
	}
	if len(domains) == 0 {
		if h.log != nil {
			h.log.Warn("证书自动续期跳过：证书没有域名")
		}
		return
	}
	renewCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	newCert, err := certRenewIssue(h, renewCtx, domains)
	if err != nil {
		if h.log != nil {
			h.log.Warn("证书自动续期失败（保留旧证书）", "err", err)
		}
		return
	}
	if newCert == nil {
		return
	}
	_ = h.putSetting(ctx, "vpn_cert_last_renew_at", itoa64(now))
	h.invalidateSettings()
	h.broadcastCert(ctx, newCert)
}

// cronCertLifecycle 把 10 分钟重试与每日续期接入统一 cron。
func (h *handlers) cronCertLifecycle(ctx context.Context, now int64) {
	h.cronCertRetry(ctx, now)
	h.cronCertRenew(ctx, now)
}
