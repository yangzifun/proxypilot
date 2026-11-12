package api

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"time"
)

// 已签发节点证书的统计口径。
//
// 旧代码查 vpn_node_certs，这张表在仓库迁移和线上库里都不存在（历史遗留的错表名），
// SQL 报错又被 `_ =` 吞掉，导致「已签发节点证书」恒为 0（审计 P2-3）。
// 真实表是 0032_vpn.sql 建的 vpn_certs（node_id + status: active|renewing|expired|revoked），
// 这里统计未吊销的证书数量。
const issuedNodeCertsSQL = `SELECT COUNT(*) FROM vpn_certs WHERE status <> 'revoked'`

func (h *handlers) countIssuedNodeCerts(ctx context.Context) (int64, error) {
	var n int64
	err := h.db.QueryRowContext(ctx, issuedNodeCertsSQL).Scan(&n)
	return n, err
}

// GET /api/vpn/ca/status —— 本站内部 CA 证书详情（管理后台「证书管理」使用）。
func (h *handlers) vpnCAStatus(w http.ResponseWriter, r *http.Request) {
	ca, err := h.vpnCA(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "error": "CA 不可用：" + err.Error(),
		})
		return
	}
	block, _ := pem.Decode(ca.CertPEM)
	if block == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "CA 证书解析失败"})
		return
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "CA 证书解析失败：" + err.Error()})
		return
	}
	sum := sha256.Sum256(cert.Raw)
	now := time.Now()
	out := map[string]any{
		"ok":          true,
		"subject":     cert.Subject.String(),
		"common_name": cert.Subject.CommonName,
		"issuer":      cert.Issuer.String(),
		"serial":      cert.SerialNumber.String(),
		"algorithm":   cert.SignatureAlgorithm.String(),
		"not_before":  cert.NotBefore.UnixMilli(),
		"not_after":   cert.NotAfter.UnixMilli(),
		"days_left":   int(cert.NotAfter.Sub(now).Hours() / 24),
		"expired":     now.After(cert.NotAfter),
		"fingerprint": hex.EncodeToString(sum[:]),
		"pem_bytes":   len(ca.CertPEM),
		"download":    "/api/vpn/ca.crt",
	}
	// 统计用该 CA 签发的节点证书数量
	issued, err := h.countIssuedNodeCerts(r.Context())
	if err != nil {
		// 不让统计失败影响整个接口，但必须留痕（旧实现静默吞错，问题被掩盖了半年）。
		if h.log != nil {
			h.log.Warn("统计节点证书数量失败", "err", err)
		}
		issued = 0
	}
	out["issued_node_certs"] = issued
	writeJSON(w, http.StatusOK, out)
}

// GET /api/vpn/cert/download?kind=cert|key|chain —— 下载当前功能证书。
func (h *handlers) vpnCertDownload(w http.ResponseWriter, r *http.Request) {
	c, err := h.loadCert(r.Context())
	if err != nil {
		writeError(w, http.StatusNotFound, "尚未签发证书")
		return
	}
	kind := r.URL.Query().Get("kind")
	var body, name, ctype string
	switch kind {
	case "key":
		body, name, ctype = c.KeyPEM, "site.key", "application/x-pem-file"
	case "chain":
		body, name, ctype = c.ChainPEM, "site-chain.pem", "application/x-pem-file"
	default:
		body, name, ctype = c.CertPEM, "site.crt", "application/x-pem-file"
	}
	if body == "" {
		writeError(w, http.StatusNotFound, "该文件不存在")
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}