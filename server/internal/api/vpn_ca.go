package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// vpnCA 加载或创建平台内部 CA（私钥 AES-GCM 加密存储）。
func (h *handlers) vpnCA(ctx context.Context) (*vpn.CA, error) {
	var certPEM, keyEnc string
	err := h.db.QueryRowContext(ctx, `SELECT cert_pem, key_enc FROM vpn_ca WHERE id = 1`).Scan(&certPEM, &keyEnc)
	if err == nil {
		keyPEM, derr := vpn.OpenBytes(h.vpnSecret(), keyEnc)
		if derr != nil {
			return nil, derr
		}
		return vpn.LoadCA([]byte(certPEM), keyPEM)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	ca, err := vpn.NewCA()
	if err != nil {
		return nil, err
	}
	enc, err := vpn.SealBytes(h.vpnSecret(), ca.KeyPEM)
	if err != nil {
		return nil, err
	}
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO vpn_ca (id, cert_pem, key_enc, created_at) VALUES (1, ?, ?, ?)`,
		string(ca.CertPEM), enc, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return ca, nil
}

// vpnIssueNodeCert 为节点域名签发内部 CA 证书。
func (h *handlers) vpnIssueNodeCert(ctx context.Context, domain string) (certPEM, keyPEM []byte, err error) {
	ca, err := h.vpnCA(ctx)
	if err != nil {
		return nil, nil, err
	}
	return ca.IssueNode(domain, 90)
}

// GET /api/vpn/ca.crt — 公开的 CA 证书（客户端信任用）。
func (h *handlers) vpnPublicCACert(w http.ResponseWriter, r *http.Request) {
	ca, err := h.vpnCA(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "CA 不可用")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(ca.CertPEM)
}
