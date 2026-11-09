// 客户端 UI：本地控制台（127.0.0.1）+ 内嵌单页界面。
//
// 设计目标：
//   - 不依赖 Node/WebView，一个二进制即可运行（Windows/Linux）；
//   - 界面风格与 masque-h3-anti-censorship 的深色控制台一致；
//   - 连接动作复用 CLI 的 门户换票 → HTTP/3 鉴权 → DNS 探测 流程，并把每一步回显到界面。
package main

import (
	"context"
		"embed"
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
	API      string `json:"api"`
	Token    string `json:"token"`
	Insecure bool   `json:"insecure"`
	DNSAddr  string `json:"dns_addr"`
	NodeID int64
	DeviceID int64  `json:"device_id"`
	// 接管系统网络：mesh=仅组网网段，full=接管全部流量
	TunMode string
	// 虚拟网卡名（留空用默认）
	TunName string `json:"tun_name"`
	// 是否把系统 DNS 指到隧道内（留空=不改）
	SetDNS bool `json:"set_dns"`
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
	raw, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, raw, 0o600)
}

// ---------- 连接引擎 ----------

type sessionState struct {
	mu        sync.Mutex
	status string // idle | connecting | connected | error
	message string
	nodeID int64
	nodeName string
	nodeHost string
	ip4 string
	meshCIDR string
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
	routes []string
	tunMode string
	tunErr string
}

func (s *sessionState) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
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
		"mesh_cidr":  s.meshCIDR,
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
		"routes":     append([]string{}, s.routes...),
		"tun_mode":   s.tunMode,
		"tun_error":  s.tunErr,
		"tun_ready":  s.adapter != "",
	}
}

func (s *sessionState) set(fn func(*sessionState)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

// ---------- UI 服务 ----------

type uiServer struct {
	demo     bool
	cfg      uiSettings
	cfgMu    sync.Mutex
	state    *sessionState
	logs     *logRing
	caBundle []byte
}

func runUI(ctx context.Context, addr string, defaults uiSettings, openBrowser, demo bool) error {
	ring := &logRing{}
	log.SetOutput(io.MultiWriter(os.Stderr, ring))

	srv := &uiServer{
		demo:  demo,
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
	mux.HandleFunc("/api/state", srv.handleState)
	mux.HandleFunc("/api/settings", srv.handleSettings)
	mux.HandleFunc("/api/nodes", srv.handleNodes)
	mux.HandleFunc("/api/connect", srv.handleConnect)
	mux.HandleFunc("/api/disconnect", srv.handleDisconnect)
	mux.HandleFunc("/api/logs", srv.handleLogs)
	mux.HandleFunc("/api/login/start", srv.handleLoginStart)
	mux.HandleFunc("/api/login/poll", srv.handleLoginPoll)
	// 安装向导
	mux.HandleFunc("/api/install/status", srv.handleInstallStatus)
	mux.HandleFunc("/api/install/start", srv.handleInstallStart)
	mux.HandleFunc("/api/install/launch", srv.handleInstallLaunch)
	mux.HandleFunc("/api/install/exit", srv.handleInstallExit)
	mux.HandleFunc("/api/logs/open", srv.handleLogsOpen)

	httpSrv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
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
	payload["version"] = Version
	writeJSON(w, payload)
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
		writeJSON(w, map[string]any{"nodes": []any{}, "error": err.Error()})
		return
	}
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
	conn := s.state.conn
	tun := s.state.tun
	s.state.cancel = nil
	s.state.conn = nil
	s.state.tun = nil
	s.state.status = "idle"
	s.state.message = reason
	s.state.startedAt = 0
	s.state.adapter = ""
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
}

func (s *uiServer) connect(nodeID int64) error {
	cfg := s.currentSettings()
	if err := cfg.validate(); err != nil {
		s.state.set(func(st *sessionState) {
			st.status = "error"
			st.message = err.Error()
		})
		return err
	}
	s.disconnect("重新连接")

	ctx, cancel := context.WithCancel(context.Background())
	s.state.set(func(st *sessionState) {
		st.status = "connecting"
		st.message = "正在拉取节点…"
		st.cancel = cancel
		st.probeOK = false
		st.probeInfo = ""
		st.rx, st.tx = 0, 0
		st.startedAt = 0
	})
	log.Printf("开始连接：面板=%s 指定节点=%d", cfg.API, nodeID)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("连接流程异常：%v", r)
				s.state.set(func(st *sessionState) { st.status = "error"; st.message = fmt.Sprint(r) })
				cancel()
			}
		}()

		nodes, err := listNodes(ctx, cfg.API, cfg.Token)
		if err != nil {
			s.fail(cancel, "拉取节点失败", err)
			return
		}
		if len(nodes) == 0 {
			s.fail(cancel, "没有可用节点", fmt.Errorf("账号未绑定节点或资格无效"))
			return
		}
		pick := nodes[0]
		for _, n := range nodes {
			if nodeID > 0 && n.ID == nodeID {
				pick = n
			}
		}
		s.state.set(func(st *sessionState) {
			st.nodeID = pick.ID
			st.nodeName = pick.Name
			st.nodeHost = pick.Endpoint
			st.message = fmt.Sprintf("已选择节点 %s，正在换票…", pick.Name)
		})
		log.Printf("选择节点：#%d %s endpoint=%s online=%v", pick.ID, pick.Name, pick.Endpoint, pick.Online)

		devID, derr := s.resolveDevice(ctx, cfg)
		if derr != nil {
			s.fail(cancel, "获取设备失败", derr)
			return
		}
		prof, err := connectPortal(ctx, cfg.API, cfg.Token, devID, pick.ID)
		if err != nil {
			s.fail(cancel, "连接接口失败", err)
			return
		}
		s.state.set(func(st *sessionState) {
			st.sessionID = prof.SessionID
			st.ip4 = prof.IP4
			st.mtu = prof.MTU
			st.meshCIDR = prof.MeshCIDR
			st.message = fmt.Sprintf("会话 %d 已建立，正在 HTTP/3 鉴权…", prof.SessionID)
		})
		log.Printf("门户会话建立：session=%d ip4=%s mtu=%d", prof.SessionID, prof.IP4, prof.MTU)

		sni := prof.TLS.ServerName
		if sni == "" {
			sni = strings.Split(pick.Endpoint, ":")[0]
		}
		// 节点自签证书时自动跳过校验（等同 CLI 的 -insecure）；换成 ACME 证书后自动走严格校验。
		skipVerify := cfg.Insecure
		if strings.EqualFold(prof.TLS.Mode, "selfsigned") || strings.EqualFold(prof.TLS.Mode, "internal") {
			skipVerify = true
			log.Printf("节点证书模式为 %s，本次连接跳过证书校验", prof.TLS.Mode)
		}
		assigned, conn, err := authHTTP3(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
		_ = conn // 仅用于鉴权；数据面走可靠流
		if err != nil {
			s.fail(cancel, "HTTP/3 鉴权失败", err)
			return
		}
		s.state.set(func(st *sessionState) {
			st.sni = sni
			st.conn = conn
			st.message = "鉴权通过，正在做 DNS 探测…"
		})
		log.Printf("HTTP/3 鉴权成功：节点分配 %s（SNI=%s）", assigned, sni)

		s.state.set(func(st *sessionState) {
			st.probeOK = true
			st.probeInfo = "鉴权通过"
			st.message = "鉴权通过，正在建立隧道…"
		})
		log.Printf("HTTP/3 鉴权通过，开始建立可靠隧道（固定 DNS=%s）", cfg.DNSAddr)

		// ---- 接管系统网络：创建虚拟网卡 + 配置地址 + 下发路由 ----
		mode := TunMode(cfg.TunMode)
		if mode != TunModeFull {
			mode = TunModeMesh
		}
		// 组网网段由节点端下发（connect 响应），客户端不再自行填写
		cidr := strings.TrimSpace(prof.MeshCIDR)
		if cidr == "" {
			cidr = strings.TrimSpace(cfg.MeshCIDR)
		}
		if cidr == "" {
			cidr = "10.99.0.0/16"
		}
		if prof.MeshCIDR != "" {
			log.Printf("采用服务端下发组网网段：%s（节点客户端子网 %s）", prof.MeshCIDR, prof.ClientCIDR)
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
		// 网卡掩码取自节点客户端子网（例如 10.88.1.0/24 → /24）
		prefixLen := 0
		if prof.ClientCIDR != "" {
			if _, netw, nerr := net.ParseCIDR(prof.ClientCIDR); nerr == nil {
				if ones, _ := netw.Mask.Size(); ones > 0 {
					prefixLen = ones
				}
			}
		}
		// 网卡 DNS 固定 1.1.1.1 与 8.8.8.8
		tunDNS := []string{"1.1.1.1", "8.8.8.8"}
		log.Printf("隧道参数：地址=%s 网关=%s 网段=%s DNS=%v", prof.IP4, gateway, cidr, tunDNS)
		pc, cleanupStream, serr := openTunnelStream(ctx, pick.Endpoint, sni, prof.Token, skipVerify)
		if serr != nil {
			s.fail(cancel, "建立隧道流失败", serr)
			return
		}
		defer cleanupStream()
		s.state.set(func(st *sessionState) { st.message = "隧道流已建立，正在接管系统网络…" })

		tun, terr := StartTunnel(ctx, TunOptions{
			Spec: TunSpec{
				Name:     cfg.TunName,
				IP:       prof.IP4,
				CIDR:     cidr,
				MTU:      prof.MTU,
				Mode:     mode,
				Endpoint:  pick.Endpoint,
				Gateway:   gateway,
				PrefixLen: prefixLen,
				DNS:       tunDNS,
			},
			Conn: pc,
			OnStats: func(rx, tx int64) {
				s.state.set(func(st *sessionState) { st.rx, st.tx = uint64(rx), uint64(tx) })
			},
		})

		if terr != nil {
			log.Printf("系统网络接管失败：%v", terr)
			s.state.set(func(st *sessionState) {
				st.status = "connected"
				st.tunErr = terr.Error()
				st.startedAt = time.Now().UnixMilli()
				st.message = "已连接，但系统网络未接管：" + terr.Error()
			})
		} else {
			res := tun.Result()
			log.Printf("系统网络已接管：%s", res.Summary())
			// 端到端连通性探测：绑定隧道地址发真实 DNS 查询
			runTunnelProbe(ctx, prof.IP4, defaultDNSAddr, func(ok bool, rtt time.Duration, errMsg string) {
				s.state.set(func(st *sessionState) {
					st.netOK = ok
					st.netRTT = int(rtt.Milliseconds())
					st.netErr = errMsg
				})
			})
			s.state.set(func(st *sessionState) {
				st.status = "connected"
				st.tun = tun
				st.adapter = res.Adapter
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
					s.state.set(func(st *sessionState) { st.rttMs = int(stats.SmoothedRTT.Milliseconds()) })
				}
				if tun != nil {
					if e := tun.Err(); e != "" {
						rx, tx := tun.Stats()
						s.state.set(func(st *sessionState) {
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

func (s *uiServer) fail(cancel context.CancelFunc, stage string, err error) {
	log.Printf("%s：%v", stage, err)
	s.state.set(func(st *sessionState) {
		st.status = "error"
		st.message = stage + "：" + err.Error()
	})
	cancel()
}

// connectPortal 调 /api/portal/vpn/connect 换连接票据。
func connectPortal(ctx context.Context, api, token string, deviceID, nodeID int64) (*profile, error) {
	body := fmt.Sprintf(`{"device_id":%d,"node_id":%d}`, deviceID, nodeID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, api+"/api/portal/vpn/connect", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
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
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
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
