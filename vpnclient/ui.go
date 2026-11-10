// 客户端 UI：本地控制台（127.0.0.1）+ 内嵌单页界面。
//
// 设计目标：
//   - 不依赖 Node/WebView，一个二进制即可运行（Windows/Linux）；
//   - 界面风格与面板控制台保持一致（深色）；隧道为自研 HTTP/3 流，非标准 MASQUE。
//   - 连接动作复用 CLI 的 门户换票 → HTTP/3 鉴权 → DNS 探测 流程，并把每一步回显到界面。
package main

import (
	"context"
		"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
)

//go:embed ui/index.html
var uiAssets embed.FS

// ---------- 日志环形缓冲 ----------

type logRing struct {
	mu    sync.Mutex
	lines []string
	seq   int64
}

func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r.seq++
		r.lines = append(r.lines, fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), line))
	}
	if len(r.lines) > 400 {
		r.lines = r.lines[len(r.lines)-400:]
	}
	return len(p), nil
}

type logLine struct {
	Seq  int64  `json:"seq"`
	Line string `json:"line"`
}

func (r *logRing) tail(since int64, limit int) []logLine {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]logLine, 0, limit)
	for i, line := range r.lines {
		seq := r.seq - int64(len(r.lines)-i) + 1
		if seq <= since {
			continue
		}
		out = append(out, logLine{Seq: seq, Line: line})
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

func (r *logRing) current() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// ---------- 设置 ----------

type uiSettings struct {
	API string `json:"api"`
	// Token 只在内存中使用；落盘时写入 TokenEnc（DPAPI 密文），不写明文。
	Token   string `json:"token,omitempty"`
	TokenEnc string `json:"token_enc,omitempty"`
	Insecure bool   `json:"insecure"`
	DNSAddr  string `json:"dns_addr"`
	NodeID int64
	// SelectedNode 记住用户在客户端里选中的节点，重启后仍然生效
	SelectedNode int64 `json:"selected_node"`
	DeviceID int64  `json:"device_id"`
	// 接管系统网络：mesh=仅组网网段，full=接管全部流量
	TunMode string
	// 虚拟网卡名（留空用默认）
	TunName string `json:"tun_name"`
	// 是否把系统 DNS 指到隧道内（留空=不改）
	SetDNS bool `json:"set_dns"`
	// KillSwitch 连接期间阻断隧道外流量（断线保护）
	KillSwitch bool `json:"kill_switch"`
	// 组网网段（默认 10.99.0.0/16）
	MeshCIDR string `json:"mesh_cidr"`
}

// configPathOverride 由 -config 指定；为空时用系统配置目录，写入失败再退到可执行文件同级。
var configPathOverride string

func settingsPath() string {
	if configPathOverride != "" {
		return configPathOverride
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "ppvpn", "config.json")
}

// ensureConfigDir 建目录；系统目录不可写时退到 exe 同级目录（便携运行）。
func ensureConfigDir(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
		return path, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return path, fmt.Errorf("无法创建配置目录")
	}
	alt := filepath.Join(filepath.Dir(exe), "ppvpn-config.json")
	if err := os.WriteFile(alt, []byte("{}"), 0o600); err != nil {
		return path, fmt.Errorf("配置目录不可写：%s", filepath.Dir(path))
	}
	return alt, nil
}

func loadSettings(defaults uiSettings) uiSettings {
	raw, err := os.ReadFile(settingsPath())
	if err != nil {
		return defaults
	}
	var s uiSettings
	if json.Unmarshal(raw, &s) != nil {
		return defaults
	}
	// 优先解密 token_enc；若只有旧版明文 token，则迁移为密文后重写文件
	if s.TokenEnc != "" {
		if plain, err := unprotectSecret(decodeB64(s.TokenEnc)); err == nil {
			s.Token = string(plain)
		} else {
			log.Printf("读取令牌失败（可能换了 Windows 用户或机器）：%v，需要重新登录", err)
		}
		s.TokenEnc = ""
	} else if s.Token != "" {
		log.Printf("检测到旧版明文令牌，正在加密存储")
		_ = saveSettings(s)
	}
	if s.API == "" {
		s.API = defaults.API
	}
	if s.DNSAddr == "" {
		s.DNSAddr = defaults.DNSAddr
	}
	return s
}

func saveSettings(s uiSettings) error {
	path, err := ensureConfigDir(settingsPath())
	if err != nil {
		return err
	}
	// 落盘前把明文令牌换成 DPAPI 密文
	if s.Token != "" {
		if enc, err := protectSecret([]byte(s.Token)); err == nil {
			s.TokenEnc = encodeB64(enc)
			s.Token = ""
		} else {
			log.Printf("加密令牌失败（将以明文保存）：%v", err)
		}
	}
	raw, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	secureFileAcl(path)
	return nil
}

// ---------- 连接引擎 ----------

type sessionState struct {
	mu        sync.Mutex
	// attempt 是连接尝试的序号：旧尝试的 goroutine 不许覆盖新尝试的状态
	attempt int64
	status string // idle | connecting | connected | error
	message string
	nodeID int64
	nodeName string
	nodeHost string
	ip4 string
	sni string
	mtu int
	sessionID int64
	probeOK bool
	probeInfo string
	rttMs int
	startedAt int64
	cancel    context.CancelFunc
	conn      *quic.Conn
	tun       *Tunnel
	rx        uint64
	tx        uint64
	adapter string
	gateway string
	netOK        bool
	netRTT       int
	netErr       string
	// 最近一次「拉节点」的结果，进诊断信息，便于区分「面板返回空」与「请求失败」
	nodesCount int
	nodesErr   string

	// 隧道质量采样（每 2 秒一次，见 quality_probe.go）
	qSent, qOK, qLost  int
	qLastRTT, qMaxRTT  int
	qConsecutiveFail   int

	// 数据面对象：用来取客户端发送队列丢弃数
	pc *streamPacketConn
	// panelBypass 记录已加直连路由的面板 IP（诊断用）
	panelBypass []string
	// 从 pc / QUIC 连接刷新出来的实时计数
	dropUplink uint64
	innerBad   uint64
	quicSent   uint64
	quicRecv   uint64
	quicLost   uint64
	routes []string
	tunMode  string
	tunErr   string
	dnsGuard  []string
	ipv6Guard      []string
	killSwitchRules []string
}

func (s *sessionState) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refreshTunnelStats()
	duration := int64(0)
	if s.startedAt > 0 && s.status == "connected" {
		duration = int64(time.Since(time.UnixMilli(s.startedAt)).Seconds())
	}
	rtt := s.rttMs
	if s.conn != nil {
		if stats := s.conn.ConnectionStats(); stats.SmoothedRTT > 0 {
			rtt = int(stats.SmoothedRTT.Milliseconds())
		}
	}
	return map[string]any{
		"status":     s.status,
		"message":    s.message,
		"node_id":    s.nodeID,
		"node_name":  s.nodeName,
		"node_host":  s.nodeHost,
		"ip4":        s.ip4,
		"sni":        s.sni,
		"mtu":        s.mtu,
		"session_id": s.sessionID,
		"probe_ok":   s.probeOK,
		"probe_info": s.probeInfo,
		"rtt_ms":     rtt,
		"duration":   duration,
		"rx":         s.rx,
		"tx":         s.tx,
		"started_at": s.startedAt,
		"adapter":    s.adapter,
		"gateway":    s.gateway,
		"net_ok":     s.netOK,
		"net_rtt_ms": s.netRTT,
		"net_error":  s.netErr,
		"panel_bypass": append([]string{}, s.panelBypass...),
		"nodes_count":  s.nodesCount,
		"nodes_error": s.nodesErr,
		"tunnel_quality": map[string]any{
			"sent": s.qSent, "ok": s.qOK, "lost": s.qLost,
			"loss_pct": pct(uint64(s.qLost), uint64(s.qSent)),
			"last_rtt_ms": s.qLastRTT, "max_rtt_ms": s.qMaxRTT,
			"consecutive_fail": s.qConsecutiveFail,
		},
		"uplink_dropped": s.dropUplink,
		"inner_bad":      s.innerBad,
		"quic_sent":      s.quicSent,
		"quic_recv":      s.quicRecv,
		"quic_lost":      s.quicLost,
		"quic_loss_pct":  pct(s.quicLost, s.quicSent),
		"routes":     append([]string{}, s.routes...),
		"tun_mode":   s.tunMode,
		"tun_error":  s.tunErr,
		"tun_ready":  s.adapter != "",
		"dns_guard":  append([]string{}, s.dnsGuard...),
		"ipv6_guard": append([]string{}, s.ipv6Guard...),
		"kill_switch_rules": append([]string{}, s.killSwitchRules...),
	}
}

// refreshTunnelStats 刷新「客户端发送队列丢弃数」与 QUIC 底层丢包计数。
// 调用方需已持有 s.mu（在 set/setFor 回调里调用）。
func (s *sessionState) refreshTunnelStats() {
	if s.pc != nil {
		s.dropUplink = s.pc.DropStats()
	}
	if s.tun != nil {
		s.innerBad = uint64(s.tun.BadInnerPackets())
	}
	if s.conn != nil {
		st := s.conn.ConnectionStats()
		s.quicSent = st.PacketsSent
		s.quicRecv = st.PacketsReceived
		s.quicLost = st.PacketsLost
	}
}

// pct 计算百分比（分母为 0 时返回 0）。
func pct(part, total uint64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) * 100 / float64(total)
}

func (s *sessionState) set(fn func(*sessionState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// ---------- UI 服务 ----------

type uiServer struct {
	listenPort string // 本地控制台监听端口，用于来源校验
	demo     bool
	cfg      uiSettings
	cfgMu    sync.Mutex
	state    *sessionState
	logs     *logRing
	caBundle []byte
}

// localGuard 给本地控制台加来源校验，防浏览器跨源静默调用。
//
// 背景：控制台监听 127.0.0.1，浏览器里的任意网页都能向它发 no-cors 的
// text/plain POST。若不校验，恶意页面可以静默把「面板地址」改成攻击者服务器
// （令牌保留），下次连接就把 Bearer 令牌发出去。
//
// 规则：
//   1. Host 必须是 127.0.0.1 / localhost / [::1]，且端口匹配
//   2. 带 Origin 的请求，Origin 必须与本机控制台同源
//   3. 写请求带 body 时，Content-Type 必须是 application/json（触发 CORS 预检）
func (s *uiServer) localGuard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		hostname := host
		port := ""
		if h, p, err := net.SplitHostPort(host); err == nil {
			hostname, port = h, p
		}
		isLocal := hostname == "127.0.0.1" || hostname == "localhost" || hostname == "::1" || hostname == "[::1]"
		if !isLocal || (s.listenPort != "" && port != s.listenPort) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}

		if origin := r.Header.Get("Origin"); origin != "" {
			want := "http://" + host
			if origin != want {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}

		if r.Method != http.MethodGet && r.ContentLength > 0 {
			ct := r.Header.Get("Content-Type")
			if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
				http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
				return
			}
		}
		next(w, r)
	}
}

func runUI(ctx context.Context, addr string, defaults uiSettings, openBrowser, demo bool) error {
	ring := &logRing{}
	log.SetOutput(io.MultiWriter(logSink(), os.Stderr, ring))

	_, listenPort, _ := net.SplitHostPort(addr)
	srv := &uiServer{
		demo:       demo,
		listenPort: listenPort,
		cfg:   loadSettings(defaults),
		state: &sessionState{status: "idle", message: "未连接"},
		logs:  ring,
	}
	if err := srv.cfg.validate(); err != nil {
		log.Printf("配置提示：%v", err)
	}

	sub, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/state", srv.localGuard(srv.handleState))
	mux.HandleFunc("/api/settings", srv.localGuard(srv.handleSettings))
	mux.HandleFunc("/api/nodes", srv.localGuard(srv.handleNodes))
	mux.HandleFunc("/api/select-node", srv.localGuard(srv.handleSelectNode))
	mux.HandleFunc("/api/logout", srv.localGuard(srv.handleLogout))
	mux.HandleFunc("/api/repair", srv.localGuard(srv.handleRepair))
	mux.HandleFunc("/api/connect", srv.localGuard(srv.handleConnect))
	mux.HandleFunc("/api/disconnect", srv.localGuard(srv.handleDisconnect))
	mux.HandleFunc("/api/logs", srv.localGuard(srv.handleLogs))
	mux.HandleFunc("/api/login/start", srv.localGuard(srv.handleLoginStart))
	mux.HandleFunc("/api/login/poll", srv.localGuard(srv.handleLoginPoll))
	// 安装向导
	mux.HandleFunc("/api/install/status", srv.localGuard(srv.handleInstallStatus))
	mux.HandleFunc("/api/install/start", srv.localGuard(srv.handleInstallStart))
	mux.HandleFunc("/api/install/launch", srv.localGuard(srv.handleInstallLaunch))
	mux.HandleFunc("/api/install/exit", srv.localGuard(srv.handleInstallExit))
	mux.HandleFunc("/api/logs/open", srv.localGuard(srv.handleLogsOpen))

	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		// 关键：进程退出前必须断开隧道并清理网卡/路由/DNS，
		// 否则用户「退出客户端」后整机流量仍被送进已消失的隧道 → 上不了网。
		log.Printf("收到退出信号，开始清理隧道…")
		srv.disconnect("客户端退出")
		log.Printf("隧道清理完成，正在关闭本地控制台")
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdown)
	}()

	url := "http://" + addr + "/"
	log.Printf("客户端控制台：%s", url)
	if openBrowser {
		go openInBrowser(url)
	}
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *uiServer) validate() error { return s.cfg.validate() }

// tokenHint 返回令牌的尾部片段（日志里用来区分不同账号，不泄露完整令牌）
func (c uiSettings) tokenHint() string {
	t := strings.TrimSpace(c.Token)
	if len(t) < 8 {
		return "(空)"
	}
	return "…" + t[len(t)-6:]
}

func (c uiSettings) validate() error {
	if strings.TrimSpace(c.API) == "" {
		return fmt.Errorf("面板地址为空")
	}
	if strings.TrimSpace(c.Token) == "" {
		return fmt.Errorf("未登录（缺少门户令牌）")
	}
	return nil
}

// defaultDNSAddr 是固定的探测 DNS（客户端不提供修改入口）。
const defaultDNSAddr = "1.1.1.1:53"

func (s *uiServer) currentSettings() uiSettings {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	cfg := s.cfg
	// 接管范围固定为「全部流量」，客户端不提供切换
	cfg.TunMode = string(TunModeFull)
	// 虚拟网卡名由平台默认值决定，不允许用户修改
	cfg.TunName = ""
	// 探测地址固定
	cfg.DNSAddr = defaultDNSAddr
	// 组网网段以服务端下发为准，这里仅作兜底
	if cfg.MeshCIDR == "" {
		cfg.MeshCIDR = "10.99.0.0/16"
	}
	return cfg
}

func (s *uiServer) handleState(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		writeJSON(w, demoState())
		return
	}
	cfg := s.currentSettings()
	payload := s.state.snapshot()
	payload["api"] = cfg.API
	payload["logged_in"] = strings.TrimSpace(cfg.Token) != ""
	payload["insecure"] = cfg.Insecure
	payload["dns_addr"] = cfg.DNSAddr
	payload["tun_mode"] = cfg.TunMode
	payload["tun_name"] = cfg.TunName
	payload["mesh_cidr"] = cfg.MeshCIDR
	payload["tun_supported"] = runtimeSupportsTun()
	payload["log_path"] = logFilePath()
	payload["log_dir"] = filepathDir(logFilePath())
	payload["platform"] = runtime.GOOS
	payload["token_hint"] = maskToken(cfg.Token)
	payload["secret_storage"] = secretStorageKind
	payload["kill_switch"] = cfg.KillSwitch
	payload["version"] = Version
	payload["selected_node"] = cfg.SelectedNode
	writeJSON(w, payload)
}

func encodeB64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func decodeB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

func maskToken(t string) string {
	t = strings.TrimSpace(t)
	if len(t) <= 10 {
		return ""
	}
	return t[:4] + "…" + t[len(t)-4:]
}

func (s *uiServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body uiSettings
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
		http.Error(w, "参数错误", http.StatusBadRequest)
		return
	}
	s.cfgMu.Lock()
	if body.API != "" {
		s.cfg.API = strings.TrimRight(strings.TrimSpace(body.API), "/")
	}
	if body.Token != "" {
		s.cfg.Token = strings.TrimSpace(body.Token)
	}
	s.cfg.KillSwitch = body.KillSwitch
	// DNS 探测地址、接管范围、网卡名一律使用固定值，不接受客户端提交
	s.cfg.TunMode = string(TunModeFull)
	s.cfg.TunName = ""
	s.cfg.DNSAddr = defaultDNSAddr
	s.cfg.Insecure = body.Insecure
	cfg := s.cfg
	s.cfgMu.Unlock()

	if err := saveSettings(cfg); err != nil {
		log.Printf("保存配置失败：%v", err)
	}
	log.Printf("配置已更新：面板=%s dns=%s insecure=%v", cfg.API, cfg.DNSAddr, cfg.Insecure)
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/repair —— 一键修复本机网络：断开隧道并清掉所有残留路由 / DNS 守卫。
func (s *uiServer) handleRepair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	log.Printf("收到「修复网络」请求，开始清理…")
	s.disconnect("手动修复网络")
	// 再执行一次启动清理，覆盖崩溃残留（适配器地址、持久化网关等）
	cleanStaleTunnel()
	log.Printf("网络修复完成")
	writeJSON(w, map[string]any{"ok": true, "message": "已清理隧道路由与 DNS 规则"})
}

// POST /api/logout —— 清除本机保存的门户令牌（退出登录）。
func (s *uiServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.cfgMu.Lock()
	s.cfg.Token = ""
	cfg := s.cfg
	s.cfgMu.Unlock()
	if err := saveSettings(cfg); err != nil {
		log.Printf("保存配置失败：%v", err)
	}
	log.Printf("已退出登录（清除本机令牌）")
	writeJSON(w, map[string]any{"ok": true})
}

// POST /api/select-node {node_id} —— 记住用户选中的节点；已连接时由前端决定是否重连。
func (s *uiServer) handleSelectNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		NodeID int64 `json:"node_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(w, "参数错误", http.StatusBadRequest)
		return
	}
	if body.NodeID <= 0 {
		http.Error(w, "node_id 非法", http.StatusBadRequest)
		return
	}
	s.cfgMu.Lock()
	s.cfg.SelectedNode = body.NodeID
	cfg := s.cfg
	s.cfgMu.Unlock()
	if err := saveSettings(cfg); err != nil {
		log.Printf("保存选中节点失败：%v", err)
	}
	log.Printf("已切换目标节点：#%d", body.NodeID)
	writeJSON(w, map[string]any{
		"ok":        true,
		"node_id":   body.NodeID,
		"connected": s.state.snapshot()["status"] == "connected",
	})
}

func (s *uiServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		writeJSON(w, map[string]any{"nodes": demoNodes()})
		return
	}
	cfg := s.currentSettings()
	if err := cfg.validate(); err != nil {
		writeJSON(w, map[string]any{"nodes": []any{}, "error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	nodes, err := listNodes(ctx, cfg.API, cfg.Token)
	if err != nil {
		log.Printf("拉取节点失败（面板 %s）：%v", cfg.API, err)
		s.state.set(func(st *sessionState) {
			st.nodesCount = 0
			st.nodesErr = err.Error()
		})
		writeJSON(w, map[string]any{"nodes": []any{}, "error": err.Error()})
		return
	}
	if len(nodes) == 0 {
		log.Printf("面板返回 0 个节点（账号=%s 面板=%s）：可能是未授权任何节点，或账号与授权不匹配",
			cfg.tokenHint(), cfg.API)
	} else {
		log.Printf("拉取节点成功：%d 个（%s）", len(nodes), cfg.API)
	}
	s.state.set(func(st *sessionState) {
		st.nodesCount = len(nodes)
		st.nodesErr = ""
	})
	writeJSON(w, map[string]any{"nodes": nodes})
}

func (s *uiServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		NodeID int64
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	if err := s.connect(body.NodeID); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *uiServer) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	s.disconnect("用户断开")
	writeJSON(w, map[string]any{"ok": true})
}

func (s *uiServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	since := int64(0)
	if v := r.URL.Query().Get("since"); v != "" {
		fmt.Sscanf(v, "%d", &since)
	}
	writeJSON(w, map[string]any{
		"lines": s.logs.tail(since, 200),
		"seq":   s.logs.current(),
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// ---------- 连接流程 ----------

func (s *uiServer) disconnect(reason string) {
	s.state.mu.Lock()
	cancel := s.state.cancel
	sessionID := s.state.sessionID
	conn := s.state.conn
	tun := s.state.tun
	s.state.cancel = nil
	s.state.conn = nil
	s.state.tun = nil
	s.state.status = "idle"
	s.state.message = reason
	s.state.startedAt = 0
	s.state.adapter = ""
	s.state.dnsGuard = nil
	s.state.ipv6Guard = nil
	s.state.killSwitchRules = nil
	s.state.gateway = ""
	s.state.netOK = false
	s.state.netRTT = 0
	s.state.netErr = ""
	s.state.routes = nil
	s.state.tunErr = ""
	s.state.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if tun != nil {
		tun.Close()
	}
	if conn != nil {
		_ = conn.CloseWithError(0, "client disconnect")
	}
	// 通知面板结束会话：否则服务端要等僵尸回收（实测两小时以上），
	// 期间会话一直算「进行中」，流量统计与并发数都会失真。
	if sessionID > 0 {
		go s.releasePanelSession(sessionID)
	}
}

// keepPanelBypassed 周期性重解析面板域名，把新出现的 IP 补成 /32 直连。
//
// 事故背景：面板在 Cloudflare 后面有两个 A 记录（如 172.67.138.128 /
// 104.21.64.253）。连接时只给当时解析到的那个加了直连路由，Cloudflare
// 轮换到另一个时，面板请求就被塞进隧道；隧道一抖就报
// net/http: TLS handshake timeout。
func (s *uiServer) keepPanelBypassed(ctx context.Context, attempt int64, apiBase string, known []string) {
	seen := map[string]bool{}
	for _, ip := range known {
		seen[ip] = true
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for _, ip := range resolveEndpointIPs(apiBase) {
			if seen[ip] {
				continue
			}
			seen[ip] = true
			if err := addBypassHostRoute(ip); err != nil {
				log.Printf("面板直连路由补充失败（%s）：%v", ip, err)
				continue
			}
			log.Printf("面板新增 IP %s，已补 /32 直连路由", ip)
			s.setFor(attempt, func(st *sessionState) {
				st.panelBypass = append(st.panelBypass, ip)
			})
		}
	}
}

// releasePanelSession 调 /api/portal/vpn/disconnect 关闭服务端会话。
func (s *uiServer) releasePanelSession(sessionID int64) {
	cfg := s.currentSettings()
	if strings.TrimSpace(cfg.API) == "" || strings.TrimSpace(cfg.Token) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	body := fmt.Sprintf("{\"session_id\":%d}", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(cfg.API, "/")+"/api/portal/vpn/disconnect", strings.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := panelHTTP.Do(req)
	if err != nil {
		log.Printf("释放面板会话 %d 失败：%v", sessionID, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	log.Printf("已释放面板会话 %d（HTTP %d）", sessionID, resp.StatusCode)
}

func (s *uiServer) connect(nodeID int64) error {
	cfg := s.currentSettings()
	if err := cfg.validate(); err != nil {
		// 还没领序号，直接写（这是最新一次调用的校验失败）
		s.state.set(func(st *sessionState) {
			st.status = "error"
			st.message = err.Error()
		})
		return err
	}
	s.disconnect("重新连接")

	// 每次连接领一个新序号：旧尝试的 goroutine 到此为止不许再改状态
	attempt := attemptSeq.Add(1)

	ctx, cancel := context.WithCancel(context.Background())
	s.state.set(func(st *sessionState) {
		st.attempt = attempt
		st.status = "connecting"
		st.message = "正在拉取节点…"
		st.cancel = cancel
		st.probeOK = false
		st.probeInfo = ""
		st.rx, st.tx = 0, 0
		st.startedAt = 0
	})
	if nodeID <= 0 {
		nodeID = cfg.SelectedNode
	}
	log.Printf("开始连接：面板=%s 指定节点=%d", cfg.API, nodeID)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("连接流程异常：%v", r)
				s.setFor(attempt, func(st *sessionState) { st.status = "error"; st.message = fmt.Sprint(r) })
				cancel()
			}
		}()

		nodes, err := listNodes(ctx, cfg.API, cfg.Token)
		if err != nil {
			s.fail(cancel, attempt, "拉取节点失败", err)
			return
		}
		if len(nodes) == 0 {
			s.fail(cancel, attempt, "没有可用节点", fmt.Errorf("账号未绑定节点或资格无效"))
			return
		}
		pick := nodes[0]
		for _, n := range nodes {
			if nodeID > 0 && n.ID == nodeID {
				pick = n
			}
		}
		s.setFor(attempt, func(st *sessionState) {
			st.nodeID = pick.ID
			st.nodeName = pick.Name
			st.nodeHost = pick.Endpoint
			st.message = fmt.Sprintf("已选择节点 %s，正在换票…", pick.Name)
		})
		// 记住本次使用的节点，下次打开客户端默认还是它
		s.cfgMu.Lock()
		if s.cfg.SelectedNode != pick.ID {
			s.cfg.SelectedNode = pick.ID
			_ = saveSettings(s.cfg)
		}
		s.cfgMu.Unlock()
		log.Printf("选择节点：#%d %s endpoint=%s online=%v", pick.ID, pick.Name, pick.Endpoint, pick.Online)

		devID, derr := s.resolveDevice(ctx, cfg)
		if derr != nil {
			s.fail(cancel, attempt, "获取设备失败", derr)
			return
		}
		prof, err := connectPortal(ctx, cfg.API, cfg.Token, devID, pick.ID)
		if err != nil {
			s.fail(cancel, attempt, "连接接口失败", err)
			return
		}
		s.setFor(attempt, func(st *sessionState) {
			st.sessionID = prof.SessionID
			st.ip4 = prof.IP4
			st.mtu = prof.MTU
			st.message = fmt.Sprintf("会话 %d 已建立，正在 HTTP/3 鉴权…", prof.SessionID)
		})
		log.Printf("门户会话建立：session=%d ip4=%s mtu=%d", prof.SessionID, prof.IP4, prof.MTU)

		sni := prof.TLS.ServerName
		if sni == "" {
			sni = strings.Split(pick.Endpoint, ":")[0]
		}
		// 每次连接都重置平台 CA：caPEM 是包级变量，若不清空，
		// 连过 internal_ca 节点后再连 ACME 节点会带着旧 CA，
		// 表现为「证书由未知机构签发」。
		caPEM = nil

		// 证书校验策略：
		//   selfsigned → 跳过校验（节点自签，无信任锚）
		//   internal_ca → 自动拉取平台 CA（/api/vpn/ca.crt）并严格校验
		//   acme        → 用系统根证书严格校验
		skipVerify := cfg.Insecure
		tlsMode := strings.ToLower(strings.TrimSpace(prof.TLS.Mode))
		switch {
		case tlsMode == "internal_ca" || tlsMode == "internal":
			if pemBytes, err := fetchPlatformCA(ctx, cfg.API); err != nil {
				log.Printf("拉取平台 CA 失败，本次退化为跳过校验：%v", err)
				skipVerify = true
			} else {
				caPEM = pemBytes
				saveCACache(pemBytes)
				log.Printf("已加载平台 CA（%d 字节），启用严格证书校验", len(pemBytes))
			}
		case tlsMode == "selfsigned" || tlsMode == "self_signed":
			skipVerify = true
			log.Printf("节点证书模式为 %s，本次连接跳过证书校验", prof.TLS.Mode)
		}
		assigned, conn, err := authHTTP3(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
		_ = conn // 仅用于鉴权；数据面走可靠流
		if err != nil {
			s.fail(cancel, attempt, "HTTP/3 鉴权失败", err)
			return
		}
		s.setFor(attempt, func(st *sessionState) {
			st.sni = sni
			st.conn = conn
			st.message = "鉴权通过，正在做 DNS 探测…"
		})
		log.Printf("HTTP/3 鉴权成功：节点分配 %s（SNI=%s）", assigned, sni)

		s.setFor(attempt, func(st *sessionState) {
			// 注意：这里不能把 probeOK 置真 —— 此时只是 HTTP/3 鉴权过了，
			// 数据面一个包都还没通。真正的判定交给 runTunnelProbe。
			st.probeOK = false
			st.probeInfo = "鉴权通过，等待数据面探测"
			st.message = "鉴权通过，正在建立隧道…"
		})
		log.Printf("HTTP/3 鉴权通过，开始建立可靠隧道（固定 DNS=%s）", cfg.DNSAddr)

		// ---- 接管系统网络：创建虚拟网卡 + 配置地址 + 下发路由 ----
		mode := TunMode(cfg.TunMode)
		if mode != TunModeFull {
			mode = TunModeMesh
		}
		// 客户端不访问组网网段：隧道只接管公网，网卡网段取节点分配的客户端子网
		cidr := strings.TrimSpace(prof.ClientCIDR)
		if cidr == "" {
			cidr = strings.TrimSpace(prof.MeshCIDR)
		}
		if cidr == "" {
			cidr = "10.99.0.0/16"
		}
		// 网关：服务端 connect 响应里的 dns[0] 就是客户端子网网关
		gateway := ""
		if len(prof.DNS) > 0 {
			gateway = strings.TrimSpace(prof.DNS[0])
		}
		if gateway == "" && prof.ClientCIDR != "" {
			if _, netw, nerr := net.ParseCIDR(prof.ClientCIDR); nerr == nil {
				if v4 := netw.IP.To4(); v4 != nil {
					gateway = net.IPv4(v4[0], v4[1], v4[2], 1).String()
				}
			}
		}
		// MTU 由管理端配置，客户端直接采用（见 netdev.go 的说明）。
		// 这里只做合理性兜底：小于 576 或大于 1500 视为配置错误，回落默认值。
		if prof.MTU < minTunMTU || prof.MTU > maxTunMTU {
			log.Printf("管理端下发的 MTU %d 超出合理范围 [%d, %d]，回落到 %d",
				prof.MTU, minTunMTU, maxTunMTU, defaultTunMTU)
			prof.MTU = defaultTunMTU
		} else {
			log.Printf("采用管理端配置的隧道 MTU：%d", prof.MTU)
		}

		// 网卡掩码取自节点客户端子网（例如 10.88.1.0/24 → /24）
		prefixLen := 0
		if prof.ClientCIDR != "" {
			if _, netw, nerr := net.ParseCIDR(prof.ClientCIDR); nerr == nil {
				if ones, _ := netw.Mask.Size(); ones > 0 {
					prefixLen = ones
				}
			}
		}
		// DNS 选择：
		//   1. 优先用节点网关（节点内置轻量 DNS 转发，就近解析、少一跳 NAT）
		//   2. 网关不可用时回落到公共 DNS
		// 8.8.8.8 在国内常被拦，用它会「连上后先卡几秒再回退」，故不使用。
		tunDNS := make([]string, 0, 3)
		if gw := strings.TrimSpace(gateway); gw != "" {
			tunDNS = append(tunDNS, gw)
		}
		tunDNS = append(tunDNS, "1.1.1.1", "223.5.5.5")
		log.Printf("隧道参数：地址=%s 网关=%s 网段=%s DNS=%v", prof.IP4, gateway, cidr, tunDNS)
		pc, cleanupStream, serr := openTunnelStream(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
		if serr != nil {
			s.fail(cancel, attempt, "建立隧道流失败", serr)
			return
		}
		defer cleanupStream()
		s.setFor(attempt, func(st *sessionState) { st.message = "隧道流已建立，正在接管系统网络…" })

		// 双通道：UDP 走 QUIC DATAGRAM（不被可靠流的队头阻塞拖累），
		// TCP/ICMP 仍走可靠流。对端没协商 DATAGRAM 时自动退回单流。
		// 双通道与 FEC 均由面板配置（协议管理 → VPN）下发，客户端不自行决定。
		fecGroup := 0
		if prof.FECMode == "xor" && prof.FECGroup > 1 {
			fecGroup = prof.FECGroup
		}
		var pconn packetConn = pc
		switch {
		case !prof.DatagramEnabled:
			log.Printf("数据面：面板已关闭 DATAGRAM，全部走可靠流")
		case !supportsDatagrams(conn):
			log.Printf("数据面：对端未协商 DATAGRAM，全部走可靠流")
		default:
			pconn = newDualPacketConn(pc, quicDatagramPeer{conn: conn}, maxDatagramPayload, fecGroup)
			if fecGroup > 1 {
				log.Printf("数据面：双通道已启用（UDP→DATAGRAM，TCP/ICMP→可靠流），FEC=xor 每组 %d 包", fecGroup)
			} else {
				log.Printf("数据面：双通道已启用（UDP→DATAGRAM，TCP/ICMP→可靠流），FEC 关闭")
			}
		}
		defer pconn.Close()

		// 解析 VPN 服务器地址：这些 IP 必须绕过隧道直连，否则会形成路由环
		endpointIPs := resolveEndpointIPs(pick.Endpoint)
		log.Printf("VPN 服务器地址解析：%v（原始 %s）", endpointIPs, pick.Endpoint)

		// 面板（控制面）同样加直连主机路由：客户端拉节点、续期、断开都靠它，
		// 不能让这些请求走进隧道。
		controlIPs := resolveEndpointIPs(cfg.API)
		if len(controlIPs) > 0 {
			log.Printf("控制面地址解析：%v（原始 %s），已排除出隧道", controlIPs, cfg.API)
		}

		tun, terr := StartTunnel(ctx, TunOptions{
			Spec: TunSpec{
				Name:        cfg.TunName,
				IP:          prof.IP4,
				CIDR:        cidr,
				MTU:         prof.MTU,
				Mode:        mode,
				Endpoint:    pick.Endpoint,
				EndpointIPs: endpointIPs,
				ControlIPs:  controlIPs,
				Gateway:     gateway,
				PrefixLen:   prefixLen,
				DNS:         tunDNS,
				KillSwitch:  s.cfg.KillSwitch,
			},
			Conn: pconn,
			OnStats: func(rx, tx int64) {
				s.setFor(attempt, func(st *sessionState) { st.rx, st.tx = uint64(rx), uint64(tx) })
			},
		})

		if terr != nil {
			log.Printf("系统网络接管失败：%v", terr)
			s.setFor(attempt, func(st *sessionState) {
				st.status = "connected"
				st.tunErr = terr.Error()
				st.startedAt = time.Now().UnixMilli()
				st.message = "已连接，但系统网络未接管：" + terr.Error()
			})
		} else {
			res := tun.Result()
			log.Printf("系统网络已接管：%s", res.Summary())
			// 端到端连通性探测：绑定隧道地址发真实 DNS 查询
			// 多目标探测：网关 + 多个公共 DNS，任一可达即视为数据面通
			runTunnelProbe(ctx, prof.IP4, defaultDNSAddr, res.Gateway, func(ok bool, rtt time.Duration, info string) {
				s.setFor(attempt, func(st *sessionState) {
					st.netOK = ok
					st.netRTT = int(rtt.Milliseconds())
					st.netErr = info
					// 徽章的「数据面正常」以真实探测为准
					st.probeOK = ok
					if ok {
						st.probeInfo = info
					} else {
						st.probeInfo = "数据面探测失败"
					}
				})
			})
			// 启动隧道质量采样（每 2 秒一次，持续到断开）
			go s.runQualityProbe(ctx, attempt, prof.IP4, res.Gateway)
			// 面板域名会轮换 IP，定期补 /32 直连路由，避免面板请求漏进隧道
			go s.keepPanelBypassed(ctx, attempt, cfg.API, controlIPs)

			s.setFor(attempt, func(st *sessionState) {
				st.status = "connected"
				st.tun = tun
				st.pc = pc
				st.adapter = res.Adapter
		st.dnsGuard = append([]string{}, res.DNSGuard...)
		st.ipv6Guard = append([]string{}, res.IPv6Disabled...)
		st.killSwitchRules = append([]string{}, res.KillSwitchRules...)
				st.gateway = res.Gateway
				st.routes = res.Routes
				st.tunMode = string(res.Mode)
				st.tunErr = ""
				st.startedAt = time.Now().UnixMilli()
				st.message = "已连接 · " + res.Summary()
			})
		}

		// 保活：保持 QUIC 连接并同步 RTT/流量，直到用户断开或隧道中断。
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if stats := conn.ConnectionStats(); stats.SmoothedRTT > 0 {
					s.setFor(attempt, func(st *sessionState) { st.rttMs = int(stats.SmoothedRTT.Milliseconds()) })
				}
				if tun != nil {
					if e := tun.Err(); e != "" {
						rx, tx := tun.Stats()
						s.setFor(attempt, func(st *sessionState) {
							st.status = "error"
							st.message = "隧道中断：" + e
							st.tunErr = e
							st.rx, st.tx = uint64(rx), uint64(tx)
						})
						cancel()
						return
					}
				}
			}
		}
	}()
	return nil
}

// attemptSeq 单调递增，用来区分「同一时刻只有最新一次连接尝试算数」。
var attemptSeq atomic.Int64

// setFor 只在「当前尝试序号」匹配时才写状态。
//
// 背景：切换节点时 connect() 会先 disconnect 再发起新尝试，旧 goroutine 的
// 请求随之被 cancel，它会立刻 fail() 写一条「拉取节点失败：context canceled」，
// 把新尝试的状态覆盖成 error —— 用户看到的就是「明明在连，却报 context canceled」。
func (s *uiServer) setFor(attempt int64, fn func(*sessionState)) {
	s.state.set(func(st *sessionState) {
		if attempt != 0 && st.attempt != attempt {
			return
		}
		fn(st)
	})
}

func (s *uiServer) fail(cancel context.CancelFunc, attempt int64, stage string, err error) {
	log.Printf("%s（尝试 %d）：%v", stage, attempt, err)
	s.setFor(attempt, func(st *sessionState) {
		st.status = "error"
		st.message = stage + "：" + err.Error()
	})
	cancel()
}

// connectPortal 调 /api/portal/vpn/connect 换连接票据。
func connectPortal(ctx context.Context, api, token string, deviceID, nodeID int64) (*profile, error) {
	body := []byte(fmt.Sprintf(`{"device_id":%d,"node_id":%d}`, deviceID, nodeID))
	url := strings.TrimRight(api, "/") + "/api/portal/vpn/connect"
	resp, err := panelDo(ctx, func(c context.Context) (*http.Request, error) {
		return panelRequest(c, http.MethodPost, url, body, token)
	}, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var p profile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// openInBrowser 用系统默认浏览器打开链接（登录授权页需要正常浏览器环境：
// 地址栏可见、Cookie/Turnstile 正常、可复用已登录的会话）。
func openInBrowser(url string) {
	if runtime.GOOS == "windows" {
		// 优先走系统关联程序（默认浏览器）
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err == nil {
			return
		}
		_ = exec.Command("cmd", "/c", "start", "", url).Start()
		return
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("打开浏览器失败（请手动访问 %s）：%v", url, err)
	}
}


// ---------- 演示数据（-ui-demo：用于预览界面，不发起真实连接） ----------

func demoNodes() []node {
	return []node{
		{ID: 13, Name: "香港2", Region: "HK", Endpoint: "hk2-node.yzfn.top:443", Online: true},
		{ID: 16, Name: "台北", Region: "TW", Endpoint: "taipei-node.yzfn.top:443", Online: true},
		{ID: 35, Name: "曼谷1", Region: "TH", Endpoint: "bkk1-node.yzfn.top:443", Online: true},
		{ID: 14, Name: "洛杉矶", Region: "US", Endpoint: "lax-node.yzfn.top:443", Online: false},
	}
}

func demoState() map[string]any {
	return map[string]any{
		"status": "connected", "message": "已连接（演示数据）",
		"node_id": 13, "node_name": "香港2", "node_host": "hk2-node.yzfn.top:443",
		"ip4": "10.88.1.7", "sni": "hk2-node.yzfn.top", "mtu": 1280, "session_id": 42,
		"probe_ok": true, "probe_info": "DNS 探测通过", "rtt_ms": 168,
		"duration": 754, "rx": 18243561, "tx": 3218790, "started_at": 0,
		"api": "https://proxypilot.yangzifun.org", "dns_addr": "1.1.1.1:53",
		"logged_in": true, "insecure": false, "token_hint": "eyJh…9xQ2",
		"version": Version, "demo": true,
	}
}

// ---------- 网页登录（浏览器授权） ----------

func (s *uiServer) panelJSON(ctx context.Context, method, path string, body any, token string) (map[string]any, error) {
	cfg := s.currentSettings()
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.API, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := panelHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("面板返回异常：%s", strings.TrimSpace(string(raw)))
	}
	if resp.StatusCode >= 300 {
		if msg, _ := out["error"].(string); msg != "" {
			return out, fmt.Errorf("%s", msg)
		}
		return out, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return out, nil
}

// POST /api/login/start → 申请授权码并打开浏览器
func (s *uiServer) handleLoginStart(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := s.panelJSON(ctx, http.MethodPost, "/api/client-auth/start", map[string]string{
		"client_name": "ProxyPilot 桌面客户端",
	}, "")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	code, _ := out["code"].(string)
	authURL, _ := out["auth_url"].(string)
	if code == "" || authURL == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "面板未返回授权地址"})
		return
	}
	log.Printf("请在浏览器完成授权：%s", authURL)
	go openInBrowser(authURL)
	writeJSON(w, map[string]any{"ok": true, "code": code, "auth_url": authURL})
}

// GET /api/login/poll?code= → 授权成功后保存令牌
func (s *uiServer) handleLoginPoll(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "缺少 code"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, err := s.panelJSON(ctx, http.MethodGet, "/api/client-auth/poll?code="+code, nil, "")
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	status, _ := out["status"].(string)
	resp := map[string]any{"ok": true, "status": status}
	if status == "approved" {
		token, _ := out["token"].(string)
		email, _ := out["email"].(string)
		if token != "" {
			s.cfgMu.Lock()
			s.cfg.Token = token
			cfg := s.cfg
			s.cfgMu.Unlock()
			if err := saveSettings(cfg); err != nil {
				log.Printf("保存登录令牌失败：%v", err)
			}
			log.Printf("网页登录成功：%s", email)
			resp["email"] = email
		}
	}
	writeJSON(w, resp)
}

// resolveDevice 取该账号可用的设备：优先用设置里的，否则拉门户设备列表取第一个可用设备。
func (s *uiServer) resolveDevice(ctx context.Context, cfg uiSettings) (int64, error) {
	if cfg.DeviceID > 0 {
		return cfg.DeviceID, nil
	}
	out, err := s.panelJSON(ctx, http.MethodGet, "/api/portal/vpn/devices", nil, cfg.Token)
	if err != nil {
		return 0, err
	}
	items, _ := out["devices"].([]any)
	var fallback int64
	for _, it := range items {
		m, _ := it.(map[string]any)
		idf, _ := m["id"].(float64)
		if idf <= 0 {
			continue
		}
		if fallback == 0 {
			fallback = int64(idf)
		}
		if st, _ := m["state"].(string); st == "active" {
			fallback = int64(idf)
			break
		}
	}
	if fallback == 0 {
		return 0, fmt.Errorf("该账号还没有设备，请先在网页端添加设备后再连接")
	}
	s.cfgMu.Lock()
	s.cfg.DeviceID = fallback
	saved := s.cfg
	s.cfgMu.Unlock()
	_ = saveSettings(saved)
	log.Printf("使用设备 #%d 连接", fallback)
	return fallback, nil
}

// POST /api/logs/open —— 在资源管理器里打开日志目录
func (s *uiServer) handleLogsOpen(w http.ResponseWriter, r *http.Request) {
	if !openLogFolder() {
		writeJSON(w, map[string]any{"ok": false, "error": "无法打开日志目录", "path": logFilePath()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": logFilePath()})
}

// filepathDir 取目录部分（避免在多个平台文件里重复引入 path/filepath）。
func filepathDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return p
}
