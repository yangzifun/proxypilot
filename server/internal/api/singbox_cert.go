package api

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// 可注入路径/命令，便于单测；生产使用面板所在主机的标准路径。
var (
	certLocalMeshDir     = "/etc/proxypilot/mesh/tls"
	certLocalVPNDir      = "/etc/proxypilot/tunnel/tls"
	certLocalSingboxDir  = "/etc/sing-box"
	certLocalRestartWait = 2 * time.Second
	certSystemctl        = func(args ...string) ([]byte, error) {
		return exec.Command("systemctl", args...).CombinedOutput()
	}
)

// validateCertKeyPair 校验 PEM 证书与私钥是否匹配（本地写入前/后使用）。
func validateCertKeyPair(certPEM, keyPEM string) error {
	cb, _ := pem.Decode([]byte(certPEM))
	if cb == nil || cb.Type != "CERTIFICATE" {
		return errors.New("证书 PEM 无效")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return fmt.Errorf("解析证书失败：%w", err)
	}
	kb, _ := pem.Decode([]byte(keyPEM))
	if kb == nil {
		return errors.New("私钥 PEM 无效")
	}
	var key any
	switch kb.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(kb.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(kb.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(kb.Bytes)
	default:
		return fmt.Errorf("不支持的私钥类型 %s", kb.Type)
	}
	if err != nil {
		return fmt.Errorf("解析私钥失败：%w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return errors.New("私钥不支持签名")
	}
	certPub, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
	if err != nil {
		return err
	}
	keyPub, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return err
	}
	if !bytes.Equal(certPub, keyPub) {
		return errors.New("证书与私钥不匹配")
	}
	return nil
}

// writeFileAtomic 临时文件 + rename，避免读取到半写证书。
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func installCertToDir(dir, certName, keyName, certPEM, keyPEM string) error {
	if dir == "" {
		return nil
	}
	if err := writeFileAtomic(filepath.Join(dir, certName), []byte(certPEM), 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(filepath.Join(dir, keyName), []byte(keyPEM), 0o600); err != nil {
		return err
	}
	// 回读并再次校验，防止落盘损坏。
	gotCert, err := os.ReadFile(filepath.Join(dir, certName))
	if err != nil {
		return err
	}
	gotKey, err := os.ReadFile(filepath.Join(dir, keyName))
	if err != nil {
		return err
	}
	return validateCertKeyPair(string(gotCert), string(gotKey))
}

func localSingboxServiceState() string {
	out, err := certSystemctl("is-active", "sing-box")
	state := strings.TrimSpace(string(out))
	if err != nil {
		if state == "" {
			return "missing"
		}
		return state
	}
	if state == "" {
		return "unknown"
	}
	return state
}

// vpnCertNodes 查询证书下发目标节点（只读，供前端展示）。
func (h *handlers) vpnCertNodes(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	cert, _ := h.loadCert(r.Context())
	targets, err := h.certTargets(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点失败")
		return
	}
	fingerprint := ""
	certStatus := "not_issued"
	if cert != nil {
		fingerprint = cert.Fingerprint
		certStatus = "unknown"
	}
	data := make([]map[string]any, 0, len(targets))
	for _, t := range targets {
		data = append(data, map[string]any{
			"id":                t.id,
			"name":              t.name,
			"domain":            t.domain,
			"singbox_installed": t.singboxInstalled,
			"singbox_service":   t.singboxService,
			"certificate":       certStatus,
			"fingerprint":       fingerprint,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

// certTarget 从 servers + 最新 server_metrics 汇总证书目标。
type certTarget struct {
	id               int64
	name             string
	domain           string
	singboxInstalled bool
	singboxService   string
}

func (h *handlers) certTargets(ctx context.Context) ([]certTarget, error) {
	rows, err := h.db.QueryContext(ctx,
		`SELECT s.id, s.name,
		        COALESCE(
		          NULLIF((SELECT n.domain FROM singbox_nodes n WHERE n.server_id = s.id AND n.domain <> '' ORDER BY n.id LIMIT 1), ''),
		          NULLIF(s.singbox_domain,''), s.host, ''),
		        COALESCE(m.services, '')
		   FROM servers s
		   LEFT JOIN server_metrics m ON m.id = (
		     SELECT MAX(id) FROM server_metrics WHERE server_id = s.id
		   )
		  WHERE s.link_mode <> 'local'
		  ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []certTarget{}
	for rows.Next() {
		var t certTarget
		var servicesJSON string
		if err := rows.Scan(&t.id, &t.name, &t.domain, &servicesJSON); err != nil {
			continue
		}
		var services map[string]string
		if json.Unmarshal([]byte(servicesJSON), &services) == nil {
			for _, key := range []string{"sing-box", "singbox"} {
				if state, ok := services[key]; ok {
					t.singboxInstalled = true
					t.singboxService = state
					break
				}
			}
		}
		if t.singboxService == "" {
			t.singboxService = "unknown"
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
