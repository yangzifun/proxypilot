package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CDN 优选健康检查：
//   - 默认 TCP 连通性（3 秒超时）
//   - 配置了「检测 SNI」后升级为 TLS 握手（校验证书），配置了路径再做一次 HTTPS 请求
//   - 连续失败 3 次自动停用，避免订阅里出现坏地址

type cdnCheckResult struct {
	ID       int64  `json:"id"`
	OK       bool   `json:"ok"`
	Latency  int    `json:"latency_ms"`
	Error    string `json:"error,omitempty"`
	Fails    int    `json:"fail_count"`
	Disabled bool   `json:"auto_disabled,omitempty"`
}

var cdnCheckMu sync.Mutex

// cdnCheckParams 从 app_settings 读取检测参数。
func (h *handlers) cdnCheckParams(ctx context.Context) (string, string) {
	var sni, path string
	_ = h.db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE `key` = 'cdn_check_sni' LIMIT 1").Scan(&sni)
	_ = h.db.QueryRowContext(ctx, "SELECT value FROM app_settings WHERE `key` = 'cdn_check_path' LIMIT 1").Scan(&path)
	return strings.TrimSpace(sni), strings.TrimSpace(path)
}

// cdnTargetAllowed 解析目标地址并拒绝内网/保留地址（审计低-02：盲 SSRF）。
//
// 之前的检测会对数据库里的任意 address:port 发起 TCP/TLS/HTTP 请求，普通管理员
// 可借此探测内网端口。这里在拨号前解析并拒绝任一私网/保留结果；解析失败也拒绝。
func cdnTargetAllowed(ctx context.Context, addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	if ip := net.ParseIP(addr); ip != nil {
		return !isBlockedIP(ip)
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(lookupCtx, "ip", addr)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return false
		}
	}
	return true
}

// probeCDNAddress 探测单个地址，返回是否可用、耗时、错误信息。
func probeCDNAddress(ctx context.Context, addr string, port int, sni, path string) (bool, int, string) {
	if !cdnTargetAllowed(ctx, addr) {
		return false, 0, "目标地址不允许（内网/保留地址或无法解析）"
	}
	target := net.JoinHostPort(addr, strconv.Itoa(port))
	start := time.Now()
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	conn, err := dialer.Dial("tcp", target)
	if err != nil {
		return false, int(time.Since(start).Milliseconds()), err.Error()
	}
	defer conn.Close()
	if sni == "" {
		return true, int(time.Since(start).Milliseconds()), ""
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS12})
	_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := tlsConn.Handshake(); err != nil {
		return false, int(time.Since(start).Milliseconds()), "TLS: " + err.Error()
	}
	if path == "" {
		return true, int(time.Since(start).Milliseconds()), ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, _ := http.NewRequest(http.MethodGet, "https://"+sni+path, nil)
	req.Host = sni
	req.Header.Set("User-Agent", "ProxyPilot-CDN-Check")
	// WS 端点需带 Upgrade 头，否则会被后端挂起直到超时
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	if err := req.Write(tlsConn); err != nil {
		return false, int(time.Since(start).Milliseconds()), "HTTP: " + err.Error()
	}
	resp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		return false, int(time.Since(start).Milliseconds()), "HTTP: " + err.Error()
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode >= 400 {
		return false, int(time.Since(start).Milliseconds()), fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, int(time.Since(start).Milliseconds()), ""
}

// runCDNPreferChecks 检测全部地址并维护连续失败计数/自动停用。
func (h *handlers) runCDNPreferChecks(ctx context.Context) ([]cdnCheckResult, error) {
	cdnCheckMu.Lock()
	defer cdnCheckMu.Unlock()

	sni, path := h.cdnCheckParams(ctx)
	rows, err := queryRowsAsMaps(ctx, h.db,
		"SELECT id, address, port, COALESCE(fail_count,0) AS fail_count, COALESCE(enabled,1) AS enabled FROM cdn_preferred_addresses LIMIT 500")
	if err != nil {
		return nil, err
	}
	results := make([]cdnCheckResult, 0, len(rows))
	now := nowMillis()
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		addr := mapStr(row, "address", "")
		port := int(mapInt(row, "port", 443))
		fails := int(mapInt(row, "fail_count", 0))
		enabled := mapInt(row, "enabled", 1)

		ok, lat, msg := probeCDNAddress(ctx, addr, port, sni, path)
		res := cdnCheckResult{ID: id, OK: ok, Latency: lat, Error: msg}
		okv := 0
		if ok {
			okv = 1
			res.Fails = 0
		} else {
			res.Fails = fails + 1
		}
		if !ok && res.Fails >= 3 && enabled == 1 {
			res.Disabled = true
			_, _ = h.db.ExecContext(ctx,
				"UPDATE cdn_preferred_addresses SET enabled = 0, last_check_at = ?, last_check_ok = 0, last_latency_ms = ?, fail_count = ? WHERE id = ?",
				now, lat, res.Fails, id)
			h.log.Warn("CDN 优选连续失败，已自动停用", "address", addr, "port", port, "fails", res.Fails)
			h.recordNodeLog(ctx, 0, 0, "cdn-prefer", "failed", 0,
				fmt.Sprintf("CDN 优选 %s:%d 连续 %d 次检测失败，已自动停用", addr, port, res.Fails), "", "cron")
		} else {
			_, _ = h.db.ExecContext(ctx,
				"UPDATE cdn_preferred_addresses SET last_check_at = ?, last_check_ok = ?, last_latency_ms = ?, fail_count = ? WHERE id = ?",
				now, okv, lat, res.Fails, id)
		}
		results = append(results, res)
	}
	ReloadCDNPreferred(ctx, h.db)
	return results, nil
}

// GET /api/cdn-preferred/settings
func (h *handlers) cdnPreferredSettingsGet(w http.ResponseWriter, r *http.Request) {
	sni, path := h.cdnCheckParams(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "sni": sni, "path": path})
}

// PUT /api/cdn-preferred/settings  {sni, path}
func (h *handlers) cdnPreferredSettingsSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SNI  string `json:"sni"`
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	for k, v := range map[string]string{"cdn_check_sni": strings.TrimSpace(body.SNI), "cdn_check_path": strings.TrimSpace(body.Path)} {
		_, _ = h.db.ExecContext(r.Context(),
			"INSERT INTO app_settings (`key`, value, updated_at) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE value = VALUES(value), updated_at = VALUES(updated_at)",
			k, v, nowMillis())
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// cronCDNPreferred 每 30 分钟自动巡检一次。
func (h *handlers) cronCDNPreferred(ctx context.Context, now int64) {
	if _, err := h.db.ExecContext(ctx,
		"INSERT INTO app_settings (`key`, value, updated_at) VALUES ('cdn_check_last_run', '0', 0) ON DUPLICATE KEY UPDATE `key` = `key`"); err != nil {
		return
	}
	var last int64
	_ = h.db.QueryRowContext(ctx, "SELECT CAST(value AS SIGNED) FROM app_settings WHERE `key` = 'cdn_check_last_run' LIMIT 1").Scan(&last)
	if now-last < 30*60*1000 {
		return
	}
	if _, err := h.db.ExecContext(ctx, "UPDATE app_settings SET value = ? WHERE `key` = 'cdn_check_last_run'", strconv.FormatInt(now, 10)); err != nil {
		return
	}
	if _, err := h.runCDNPreferChecks(ctx); err != nil {
		h.log.Warn("CDN 优选定时检测失败", "err", err)
	}
}
