package main

import (
	"crypto/x509"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// rootPoolWithPlatformCA 返回「系统根证书 + 平台内部 CA」组成的信任池。
//
// 之前是把 RootCAs 直接替换成「只含平台 CA」的新池子，后果是：
// 一旦客户端拉过内部 CA（连过 internal_ca 节点），再连 ACME 节点就会报
//   x509: certificate signed by unknown authority
// 因为系统根证书被整个丢掉了。正确做法是追加，而不是替换。
func rootPoolWithPlatformCA() (*x509.CertPool, error) {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if len(caPEM) > 0 {
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("CA 证书无效")
		}
	}
	return pool, nil
}

// fetchPlatformCA 拉取平台内部 CA 证书（公开接口，无需登录）。
func fetchPlatformCA(ctx context.Context, apiBase string) ([]byte, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if base == "" {
		return nil, fmt.Errorf("未配置面板地址")
	}
	url := base + "/api/vpn/ca.crt"
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := panelHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	if !strings.Contains(string(body), "BEGIN CERTIFICATE") {
		return nil, fmt.Errorf("返回内容不是 PEM 证书")
	}
	return body, nil
}

// saveCACache 缓存平台 CA，平台不可达时可用上次的证书。
func saveCACache(pemBytes []byte) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, "ProxyPilot", "ca.pem")
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, pemBytes, 0o600)
}

// loadCACache 读取上次缓存的平台 CA。
func loadCACache() []byte {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, "ProxyPilot", "ca.pem"))
	if err != nil {
		return nil
	}
	return b
}
