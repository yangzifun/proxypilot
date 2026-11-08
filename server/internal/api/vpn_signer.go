package api

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/vpn"
)

// vpnSecret 用于加密 VPN 签名私钥（优先 AgentKeySecret，回落到 JWTSecret）。
func (h *handlers) vpnSecret() string {
	if s := strings.TrimSpace(h.cfg.AgentKeySecret); s != "" {
		return s
	}
	return h.cfg.JWTSecret
}

// vpnSigner 加载或生成 Ed25519 连接令牌签名密钥。
func (h *handlers) vpnSigner(ctx context.Context) (*vpn.Signer, error) {
	var kid, pub, enc string
	err := h.db.QueryRowContext(ctx,
		`SELECT kid, public_key, private_key_enc FROM vpn_signing_keys
		  WHERE active = 1 ORDER BY created_at DESC LIMIT 1`).Scan(&kid, &pub, &enc)
	if err == nil {
		priv, err := vpn.OpenPrivateKey(h.vpnSecret(), enc)
		if err != nil {
			return nil, err
		}
		return vpn.NewSigner(kid, priv)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	s, err := vpn.GenerateSigner()
	if err != nil {
		return nil, err
	}
	encPriv, err := vpn.SealPrivateKey(h.vpnSecret(), s.PrivateKey())
	if err != nil {
		return nil, err
	}
	if _, err := h.db.ExecContext(ctx,
		`INSERT INTO vpn_signing_keys (kid, public_key, private_key_enc, active, created_at)
		 VALUES (?, ?, ?, 1, ?)`,
		s.Kid(), vpn.PublicKeyBase64(s.PublicKey()), encPriv, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	return s, nil
}
