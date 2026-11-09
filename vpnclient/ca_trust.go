package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	resp, err := http.DefaultClient.Do(req)
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
