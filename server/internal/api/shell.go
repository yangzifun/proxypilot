package api

import (
	"fmt"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/yzfn/proxypilot/server/internal/auth"
)

type shellState struct {
	serverID int64
	session  string
	browser  *wsConn
	agent    *wsConn
	closed   bool
	timer    *time.Timer
}

var shellHub = struct {
	sync.Mutex
	sessions map[string]*shellState
	byServer map[int64]string
}{sessions: map[string]*shellState{}, byServer: map[int64]string{}}

func randomSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *handlers) saveShellSession(ctx context.Context, serverID int64, session string) {
	now := nowMillis()
	_, _ = h.db.ExecContext(ctx, `INSERT INTO shell_sessions (server_id, session, created_at, status)
	  VALUES (?, ?, ?, 'waiting')
	  ON DUPLICATE KEY UPDATE session = VALUES(session), created_at = VALUES(created_at), status = 'waiting'`,
		serverID, session, now)
}

func (h *handlers) dropShellSession(ctx context.Context, serverID int64, session string) {
	_, _ = h.db.ExecContext(ctx, `DELETE FROM shell_sessions WHERE server_id = ? AND session = ?`, serverID, session)
}

// pendingShellSession 供 Agent poll 领取会话。
func (h *handlers) pendingShellSession(ctx context.Context, serverID int64) (string, bool) {
	var session string
	var createdAt int64
	err := h.db.QueryRowContext(ctx,
		`SELECT session, COALESCE(created_at,0) FROM shell_sessions WHERE server_id = ? AND status = 'waiting' LIMIT 1`,
		serverID).Scan(&session, &createdAt)
	if err != nil || session == "" {
		return "", false
	}
	if createdAt > 0 && nowMillis()-createdAt > 90_000 {
		_, _ = h.db.ExecContext(ctx, `DELETE FROM shell_sessions WHERE server_id = ?`, serverID)
		return "", false
	}
	return session, true
}

func (s *shellState) closeAll(code uint16, reason string) {
	if s.closed {
		return
	}
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	if s.browser != nil {
		s.browser.writeClose(code, reason)
	}
	if s.agent != nil {
		s.agent.writeClose(code, reason)
	}
}

// GET /api/shell/status
func (h *handlers) shellStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"durable": true, "available": true, "agent_min": "1.3.0",
		"mode": "go-websocket-relay",
	})
}

// GET /api/shell/browser/{id}?token=<JWT>
func (h *handlers) shellBrowser(w http.ResponseWriter, r *http.Request) {
	serverID := jsonInt(pathParam(r, "id"))
	if serverID <= 0 {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		token = strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	}
	if token == "" {
		writeError(w, http.StatusUnauthorized, "缺少登录令牌")
		return
	}
	claims, err := auth.Parse(token, h.cfg.JWTSecret)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "登录已过期，请重新登录")
		return
	}
	if claims.Role == "user" {
		writeError(w, http.StatusForbidden, "用户端令牌不能使用管理终端")
		return
	}
	var linkMode string
	var allowExec int64
	var exists int64
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*), COALESCE(MAX(link_mode),'agent'), COALESCE(MAX(allow_exec),0) FROM servers WHERE id = ?`,
		serverID).Scan(&exists, &linkMode, &allowExec); err != nil || exists == 0 {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	if linkMode != "agent" && linkMode != "local" {
		writeError(w, http.StatusBadRequest, "该服务器不是 Agent / 本地接入方式")
		return
	}
	if allowExec != 1 {
		writeError(w, http.StatusForbidden, "该服务器未开启「允许执行命令」")
		return
	}

	if !h.wsOriginAllowed(r) {
		writeError(w, http.StatusForbidden, "WebSocket Origin 不被允许")
		return
	}
	ws, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	stopPing := make(chan struct{})
	defer close(stopPing)
	go ws.keepAlive(stopPing)
	if linkMode == "local" {
		_ = ws.WriteText(`{"type":"ready"}`)
		h.serveLocalShell(ws)
		return
	}
	session := randomSessionID()
	h.saveShellSession(r.Context(), serverID, session)

	state := &shellState{serverID: serverID, session: session, browser: ws}
	shellHub.Lock()
	if oldID, ok := shellHub.byServer[serverID]; ok {
		if old := shellHub.sessions[oldID]; old != nil {
			old.closeAll(4001, "replaced")
			delete(shellHub.sessions, oldID)
		}
	}
	shellHub.sessions[session] = state
	shellHub.byServer[serverID] = session
	shellHub.Unlock()

	// 先通过常驻 WS 把会话直接推给 Agent（长连接模式下即时接入）
	pushed := pushAgentText(serverID, `{"type":"shell","session":"`+session+`"}`)
	// 再唤醒等待中的 Agent；没有 WS 的旧 Agent 会在下次 poll 领到 session
	wakeAgent(serverID)
	if pushed {
		// Agent 长连接在线，已直接下发，无需等待轮询
		_ = ws.WriteText(`{"type":"pushed"}`)
	} else {
		_ = ws.WriteText(`{"type":"waiting"}`)
	}

	state.timer = time.AfterFunc(60*time.Second, func() {
		shellHub.Lock()
		cur := shellHub.sessions[session]
		if cur != nil && cur.agent == nil {
			cur.closeAll(4002, "agent timeout")
			delete(shellHub.sessions, session)
			if shellHub.byServer[serverID] == session {
				delete(shellHub.byServer, serverID)
			}
		}
		shellHub.Unlock()
		h.dropShellSession(context.Background(), serverID, session)
	})

	defer func() {
		shellHub.Lock()
		if cur := shellHub.sessions[session]; cur != nil {
			cur.closeAll(1000, "browser closed")
			delete(shellHub.sessions, session)
		}
		if shellHub.byServer[serverID] == session {
			delete(shellHub.byServer, serverID)
		}
		shellHub.Unlock()
		h.dropShellSession(context.Background(), serverID, session)
	}()

	for {
		op, payload, err := ws.ReadMessage()
		if err != nil {
			if err != io.EOF {
				_ = ws.WriteText(`{"type":"error","message":"终端连接已断开"}`)
			}
			break
		}
		shellHub.Lock()
		agent := state.agent
		shellHub.Unlock()
		if agent == nil {
			continue
		}
		if op == 0x2 {
			_ = agent.WriteBinary(payload)
		} else {
			_ = agent.WriteText(string(payload))
		}
	}
	shellHub.Lock()
	if state.agent != nil {
		_ = state.agent.WriteText(`{"type":"exit"}`)
	}
	shellHub.Unlock()
}

// GET /api/shell/agent/{id}?session=
func (h *handlers) shellAgent(w http.ResponseWriter, r *http.Request) {
	serverID := jsonInt(pathParam(r, "id"))
	srv, status, msg := h.agentAuthenticate(r, nil)
	if srv == nil {
		writeError(w, status, msg)
		return
	}
	if srv.ID != serverID {
		writeError(w, http.StatusForbidden, "令牌与服务器不匹配")
		return
	}
	session := strings.TrimSpace(r.URL.Query().Get("session"))
	if session == "" {
		writeError(w, http.StatusBadRequest, "缺少会话号")
		return
	}
	var dbSession string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT session FROM shell_sessions WHERE server_id = ? LIMIT 1`, serverID).Scan(&dbSession); err != nil || dbSession != session {
		writeError(w, http.StatusGone, "会话不存在或已过期")
		return
	}
	shellHub.Lock()
	state := shellHub.sessions[session]
	shellHub.Unlock()
	if state == nil {
		writeError(w, http.StatusGone, "会话不存在或已过期")
		return
	}

	ws, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	stopPing := make(chan struct{})
	defer close(stopPing)
	go ws.keepAlive(stopPing)
	shellHub.Lock()
	if state.agent != nil {
		state.agent.writeClose(4001, "replaced")
	}
	state.agent = ws
	if state.timer != nil {
		state.timer.Stop()
	}
	browser := state.browser
	shellHub.Unlock()

	_, _ = h.db.ExecContext(r.Context(),
		`UPDATE shell_sessions SET status = 'active' WHERE server_id = ? AND session = ?`, serverID, session)
	_ = ws.WriteText(`{"type":"hello"}`)
	if browser != nil {
		_ = browser.WriteText(`{"type":"ready"}`)
	}

	defer func() {
		shellHub.Lock()
		state.agent = nil
		state.closed = true
		delete(shellHub.sessions, session)
		if shellHub.byServer[serverID] == session {
			delete(shellHub.byServer, serverID)
		}
		shellHub.Unlock()
		if browser != nil {
			_ = browser.WriteText(`{"type":"exit"}`)
		}
		h.dropShellSession(context.Background(), serverID, session)
	}()

	for {
		op, payload, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if browser == nil {
			continue
		}
		if op == 0x2 {
			_ = browser.WriteBinary(payload)
		} else {
			_ = browser.WriteText(string(payload))
		}
	}
}

// ---- Agent WebSocket（唤醒通道） ----

var agentWSHub = struct {
	sync.Mutex
	conns map[int64]*wsConn
}{conns: map[int64]*wsConn{}}

// pushAgentText 通过 Agent 常驻 WebSocket 推送一条文本消息（未连接则忽略）。
func pushAgentText(serverID int64, text string) bool {
	agentWSHub.Lock()
	ws := agentWSHub.conns[serverID]
	agentWSHub.Unlock()
	if ws == nil {
		return false
	}
	return ws.WriteText(text) == nil
}

func wakeAgent(serverID int64) {
	wakeWaiters(serverID)
	agentWSHub.Lock()
	ws := agentWSHub.conns[serverID]
	agentWSHub.Unlock()
	if ws != nil {
		_ = ws.WriteText("wake")
	}
}

// GET /api/agent/ws
func (h *handlers) agentWS(w http.ResponseWriter, r *http.Request) {
	srv, status, msg := h.agentAuthenticate(r, nil)
	if srv == nil {
		writeError(w, status, msg)
		return
	}
	ws, ok := upgradeWS(w, r)
	if !ok {
		return
	}
	stopPing := make(chan struct{})
	defer close(stopPing)
	go ws.keepAlive(stopPing)
	agentWSHub.Lock()
	if old := agentWSHub.conns[srv.ID]; old != nil {
		old.writeClose(4001, "replaced")
	}
	agentWSHub.conns[srv.ID] = ws
	agentWSHub.Unlock()

	_ = ws.WriteText("wake")
	// 端点信息：连接建立时下发，并每 30 秒复读一次设置，便于在线修改后立即生效
	sendEndpoint := func() {
		inner := strings.TrimSpace(h.settingValue(r.Context(), settingAgentInternalURL))
		outer := strings.TrimSpace(h.settingValue(r.Context(), settingAgentAccessURL))
		if inner == "" && outer == "" {
			return
		}
		_ = ws.WriteText(fmt.Sprintf(`{"type":"endpoint","internal":%q,"external":%q}`, inner, outer))
	}
	sendEndpoint()
	endpointTicker := time.NewTicker(30 * time.Second)
	defer endpointTicker.Stop()
	go func() {
		for {
			select {
			case <-stopPing:
				return
			case <-endpointTicker.C:
				sendEndpoint()
			}
		}
	}()
	defer func() {
		agentWSHub.Lock()
		if agentWSHub.conns[srv.ID] == ws {
			delete(agentWSHub.conns, srv.ID)
		}
		agentWSHub.Unlock()
	}()
	remoteIP := clientIP(r)
	for {
		op, body, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if op != 0x1 || len(body) == 0 || body[0] != '{' {
			continue
		}
		var frame map[string]any
		if json.Unmarshal(body, &frame) != nil {
			continue
		}
		switch mapStr(frame, "type", "") {
		case "metrics":
			if sample, ok := frame["sample"].(map[string]any); ok {
				h.ingestAgentWSMetrics(r.Context(), srv, sample, remoteIP)
			}
		case "wake":
			// 老格式兼容：Agent 主动要任务时也唤醒自己
		}
	}
}
func (h *handlers) wsOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true // 非浏览器客户端（Agent / CLI）
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if pb := h.publicBaseURL(r.Context()); pb != "" {
		if pu, err := url.Parse(pb); err == nil && strings.EqualFold(pu.Host, u.Host) {
			return true
		}
	}
	return false
}

func (h *handlers) serveLocalShell(ws *wsConn) {
	shell := "script"
	args := []string{"-q", "-c", "/bin/bash -il", "/dev/null"}
	if _, err := exec.LookPath("script"); err != nil {
		shell = "/bin/bash"
		args = []string{"-il"}
	}
	cmd := exec.Command(shell, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "PROXYPILOT_SHELL=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = ws.WriteText(`{"type":"error","message":"无法创建终端"}`)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = ws.WriteText(`{"type":"error","message":"无法创建终端"}`)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		_ = ws.WriteText(`{"type":"error","message":"启动终端失败"}`)
		return
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()
	writeOut := func(rd io.Reader) {
		buf := make([]byte, 8192)
		for {
			n, err := rd.Read(buf)
			if n > 0 {
				_ = ws.WriteBinary(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	go writeOut(stdout)
	go writeOut(stderr)
	for {
		op, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if op == 0x2 {
			if _, err := stdin.Write(data); err != nil {
				return
			}
		}
	}
}
