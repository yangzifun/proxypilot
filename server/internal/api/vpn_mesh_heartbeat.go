package api

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// POST /api/vpn/mesh/heartbeat — 组网客户端上报存活（Bearer 组网 JWT）。
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
	ip := strings.TrimSpace(claims.IPv4)
	now := time.Now().UnixMilli()
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE vpn_mesh_nodes SET state = 'up', last_seen = ?, updated_at = ? WHERE mesh_ip4 = ?`,
		now, now, ip)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "更新失败")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "组网成员不存在")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ip": ip})
}
