package api

import (
	"bufio"
	"context"
	"crypto/tls"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// =====================================================================
//  CDN 优选「单地址测试」接口
//
//  POST /api/cdn-preferred/test（authed 管理员）
//    body 二选一：{"id":123} 或 {"address":"162.159.198.1","port":443,"sni":"...","path":"/vlws"}
//  - 由**面板进程本身**发起 TCP→TLS→HTTP 测试，不经过节点/Agent；
//  - 复用 cdnTargetAllowed 的 SSRF 防护（解析后拒绝内网/保留地址）；
//  - sni/path 不传时取全局检测设置（cdn_check_sni / cdn_check_path）；
//  - 测试失败仍 HTTP 200（ok=false + error 说明阶段），参数错误 400、id 不存在 404。
// =====================================================================

// cdnProbeDetail 单地址测试的完整阶段结果（也是响应 data 的契约）。
type cdnProbeDetail struct {
	Address    string   `json:"address"`
	Port       int      `json:"port"`
	Resolved   []string `json:"resolved"`
	OK         bool     `json:"ok"`
	LatencyMS  int      `json:"latency_ms"`
	TCPOK      bool     `json:"tcp_ok"`
	TLSOK      bool     `json:"tls_ok"`
	HTTPStatus int      `json:"http_status"`
	Error      string   `json:"error"`
}

// cdnProbeTransport 把 TCP / TLS / HTTP 三阶段抽成可注入函数：
// 生产走真实网络，单测替换后无需联网即可覆盖各阶段失败与成功路径。
type cdnProbeTransport struct {
	Dial func(ctx context.Context, target string) (net.Conn, error)
	TLS  func(conn net.Conn, sni string) (net.Conn, error)
	HTTP func(conn net.Conn, sni, path string) (int, error)
}

// resolveCDNAddress 解析目标地址：IP 直接返回，域名返回全部 A/AAAA。
func resolveCDNAddress(ctx context.Context, addr string) ([]string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("地址为空")
	}
	if ip := net.ParseIP(addr); ip != nil {
		return []string{ip.String()}, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(lookupCtx, "ip", addr)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("解析结果为空")
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out, nil
}

// cdnResolvedAllowed 解析结果必须全部是公网地址（防 SSRF / DNS rebinding）。
func cdnResolvedAllowed(resolved []string) bool {
	if len(resolved) == 0 {
		return false
	}
	for _, raw := range resolved {
		ip := net.ParseIP(strings.TrimSpace(raw))
		if ip == nil || isBlockedIP(ip) {
			return false
		}
	}
	return true
}

// cdnTestResolve 解析函数注入点（单测替换）。
var cdnTestResolve = resolveCDNAddress

// cdnTestNet 网络三阶段实现（单测可整体替换）。
var cdnTestNet = cdnProbeTransport{
	Dial: func(ctx context.Context, target string) (net.Conn, error) {
		dialer := &net.Dialer{Timeout: 3 * time.Second}
		return dialer.DialContext(ctx, "tcp", target)
	},
	TLS: func(conn net.Conn, sni string) (net.Conn, error) {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS12})
		_ = tlsConn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := tlsConn.Handshake(); err != nil {
			return nil, err
		}
		return tlsConn, nil
	},
	HTTP: func(conn net.Conn, sni, path string) (int, error) {
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req, err := http.NewRequest(http.MethodGet, "https://"+sni+path, nil)
		if err != nil {
			return 0, err
		}
		req.Host = sni
		req.Header.Set("User-Agent", "ProxyPilot-CDN-Check")
		// WS 端点需带 Upgrade 头，否则会被后端挂起直到超时
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-WebSocket-Version", "13")
		req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
		if err := req.Write(conn); err != nil {
			return 0, err
		}
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	},
}

// probeCDNAddressDetail 对单个地址做 TCP→TLS→HTTP 分阶段探测。
//
// 判定口径（与批量检测一致）：
//   - 目标解析失败/命中内网 → ok=false，error=目标不允许；
//   - TCP 失败 → tcp_ok=false，error=TCP: ...；
//   - 未配置 SNI → 只做 TCP 连通性检查，ok=true、tls_ok=false、http_status=0；
//   - SNI 配置但未配置 path → TLS 成功即 ok=true；
//   - path 配置后返回状态码：101/&lt;400 视为成功，其余 ok=false、error=HTTP xxx。
func probeCDNAddressDetail(ctx context.Context, addr string, port int, sni, path string) cdnProbeDetail {
	out := cdnProbeDetail{
		Address:  strings.TrimSpace(addr),
		Port:     port,
		Resolved: []string{},
	}
	start := time.Now()
	finish := func() cdnProbeDetail {
		out.LatencyMS = int(time.Since(start).Milliseconds())
		return out
	}

	resolved, err := cdnTestResolve(ctx, out.Address)
	if err != nil {
		out.Error = "目标地址不允许（内网/保留地址或无法解析）"
		return finish()
	}
	out.Resolved = resolved
	if !cdnResolvedAllowed(resolved) {
		out.Error = "目标地址不允许（内网/保留地址或无法解析）"
		return finish()
	}

	target := net.JoinHostPort(out.Address, strconv.Itoa(port))
	conn, err := cdnTestNet.Dial(ctx, target)
	if err != nil {
		out.Error = "TCP: " + err.Error()
		return finish()
	}
	defer conn.Close()
	out.TCPOK = true

	sni = strings.TrimSpace(sni)
	if sni == "" {
		out.OK = true
		return finish()
	}
	tlsConn, err := cdnTestNet.TLS(conn, sni)
	if err != nil {
		out.Error = "TLS: " + err.Error()
		return finish()
	}
	defer tlsConn.Close()
	out.TLSOK = true

	path = strings.TrimSpace(path)
	if path == "" {
		out.OK = true
		return finish()
	}
	status, err := cdnTestNet.HTTP(tlsConn, sni, path)
	if err != nil {
		out.Error = "HTTP: " + err.Error()
		return finish()
	}
	out.HTTPStatus = status
	if status != http.StatusSwitchingProtocols && status >= 400 {
		out.Error = fmt.Sprintf("HTTP %d", status)
		return finish()
	}
	out.OK = true
	return finish()
}

// POST /api/cdn-preferred/test —— 单地址即时测试（面板本机发起）。
func (h *handlers) cdnPreferredTest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      *int64  `json:"id"`
		Address string  `json:"address"`
		Port    *int    `json:"port"`
		SNI     *string `json:"sni"`
		Path    *string `json:"path"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}

	addr := strings.TrimSpace(body.Address)
	port := 443
	if body.ID != nil && *body.ID > 0 {
		var dbAddr string
		var dbPort int
		err := h.db.QueryRowContext(r.Context(),
			"SELECT address, port FROM cdn_preferred_addresses WHERE id = ?", *body.ID).
			Scan(&dbAddr, &dbPort)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "记录不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询失败")
			return
		}
		if addr == "" {
			addr = strings.TrimSpace(dbAddr)
		}
		if body.Port == nil && dbPort > 0 {
			port = dbPort
		}
	}
	if addr == "" {
		writeError(w, http.StatusBadRequest, "address 或 id 必填")
		return
	}
	if body.Port != nil {
		port = *body.Port
	}
	if port <= 0 || port > 65535 {
		writeError(w, http.StatusBadRequest, "port 无效（1-65535）")
		return
	}

	// sni/path 未传（nil）时取全局检测设置；显式传空串表示跳过该阶段。
	globalSNI, globalPath := h.cdnCheckParams(r.Context())
	sni := globalSNI
	if body.SNI != nil {
		sni = strings.TrimSpace(*body.SNI)
	}
	path := globalPath
	if body.Path != nil {
		path = strings.TrimSpace(*body.Path)
	}

	detail := probeCDNAddressDetail(r.Context(), addr, port, sni, path)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": detail})
}
