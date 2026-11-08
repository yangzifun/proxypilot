package api

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// meshHeartbeatBody 是组网客户端的可选上报体；老客户端发空 body 也要兼容。
type meshHeartbeatBody struct {
	RTTms     int    `json:"rtt_ms"`
	Transport string `json:"transport"`
	State     string `json:"state"`
	Endpoint  string `json:"endpoint"`
	LastError string `json:"last_error"`
}

// POST /api/vpn/mesh/heartbeat — 组网客户端上报存活、RTT 与数据面状态（Bearer 组网 JWT）。
func (h *handlers) vpnMeshHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(raw, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	signer, err := h.vpnSigner(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "签名密钥不可用")
		return
	}
	claims, err := vpn.Verify(strings.TrimSpace(strings.TrimPrefix(raw, "Bearer ")), func(kid string) (ed25519.PublicKey, error) {
		if kid != signer.Kid() {
			return nil, errors.New("unknown kid")
		}
		return signer.PublicKey(), nil
	}, time.Now())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	var body meshHeartbeatBody
	if r.Body != nil {
		// 解析失败按老客户端处理（空 body / 非 JSON 都不影响存活上报）。
		_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body)
	}
	rtt := body.RTTms
	if rtt < 0 {
		rtt = 0
	}
	if rtt > 60000 {
		rtt = 60000
	}
	state := "up"
	if strings.EqualFold(body.State, "down") {
		state = "down"
	}
	transport := strings.TrimSpace(body.Transport)
	if transport == "" {
		transport = "masque"
	}
	if len(transport) > 16 {
		transport = transport[:16]
	}

	ip := strings.TrimSpace(claims.IPv4)
	now := time.Now().UnixMilli()

	update := func(where string, key any) (int64, error) {
		res, err := h.db.ExecContext(r.Context(),
			`UPDATE vpn_mesh_nodes SET state = ?, last_seen = ?, updated_at = ?, rtt_ms = ?, transport = ?
			  WHERE `+where, state, now, now, rtt, transport, key)
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		return n, nil
	}

	// 1) 优先按令牌中的组网 IP 匹配（节点实际使用的地址）。
	n := int64(0)
	if ip != "" {
		n, err = update("mesh_ip4 = ?", ip)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
	}
	// 2) IP 漂移时退回按 server_id 匹配（仅在成员尚未登记 IP 时纠正地址，
	//    避免旧令牌里的错误 node 声明把别人的登记地址改掉）。
	if n == 0 && claims.NodeID > 0 {
		n, err = update("server_id = ?", claims.NodeID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
		if n > 0 && ip != "" {
			_, _ = h.db.ExecContext(r.Context(),
				`UPDATE vpn_mesh_nodes SET mesh_ip4 = ? WHERE server_id = ? AND mesh_ip4 = ''`, ip, claims.NodeID)
		}
	}

	// 3) 老成员不存在时自动补登，避免"心跳一直 404、面板永远 down"。
	if n == 0 && claims.NodeID > 0 && ip != "" {
		domain := ""
		if s, serr := h.vpnStore().LoadSettings(r.Context()); serr == nil {
			domain = s.ServerMeshDomain
		}
		_, err = h.db.ExecContext(r.Context(),
			`INSERT INTO vpn_mesh_nodes (server_id, mesh_ip4, domain, state, transport, rtt_ms, last_seen, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE mesh_ip4 = VALUES(mesh_ip4), domain = VALUES(domain), state = VALUES(state),
			                         transport = VALUES(transport), rtt_ms = VALUES(rtt_ms),
			                         last_seen = VALUES(last_seen), updated_at = VALUES(updated_at)`,
			claims.NodeID, ip, domain, state, transport, rtt, now, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "补登组网成员失败")
			return
		}
		n = 1
	}

	if n == 0 {
		// 令牌既无 IP 也无 server_id：无从判断归属。
		writeError(w, http.StatusNotFound, "组网成员不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ip": ip, "rtt_ms": rtt, "state": state})
}
