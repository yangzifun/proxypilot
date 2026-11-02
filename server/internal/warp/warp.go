// Package warp 负责在控制面注册/删除 Cloudflare WARP(MASQUE) 设备。
// 注册接口是非公开 API，字段与版本号集中在本文件，便于上游变化时替换。
package warp

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL       = "https://api.cloudflareclient.com"
	DefaultVersion       = "v0a2158"
	DefaultCFClient      = "a-6.30.0"
	DefaultUserAgent     = "okhttp/3.12.1"
	defaultRequestTimout = 20 * time.Second
)

// ErrBlocked 表示注册接口拒绝请求（频控/风控/接口变更）。
var ErrBlocked = errors.New("WARP 注册接口拒绝请求")

// Profile 是一次 WARP 设备注册的结果。
type Profile struct {
	DeviceID      string
	Token         string
	PrivateKey    string // 客户端 X25519 私钥（base64）
	PublicKey     string // 客户端 X25519 公钥（base64）
	PeerPublicKey string // WARP 服务端公钥（base64）
	IPv4          string
	IPv6          string
	Endpoint      string // 注册返回的 WireGuard endpoint，仅参考
}

// Client 是 WARP 注册客户端。
type Client struct {
	HTTP      *http.Client
	BaseURL   string
	Version   string
	CFClient  string
	UserAgent string
}

// New 返回使用默认端点的客户端。
func New() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: defaultRequestTimout},
		BaseURL:   DefaultBaseURL,
		Version:   DefaultVersion,
		CFClient:  DefaultCFClient,
		UserAgent: DefaultUserAgent,
	}
}

type regPeer struct {
	PublicKey string `json:"public_key"`
	Endpoint  struct {
		Host string `json:"host"`
	} `json:"endpoint"`
}

type regResponse struct {
	ID     string `json:"id"`
	Token  string `json:"token"`
	Config struct {
		Peers     []regPeer `json:"peers"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
	} `json:"config"`
}

// Register 生成一对 X25519 密钥并向 Cloudflare 注册设备。
func (c *Client) Register(ctx context.Context) (Profile, error) {
	if c == nil {
		return Profile{}, errors.New("warp client 为空")
	}
	curve := ecdh.X25519()
	priv, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return Profile{}, fmt.Errorf("生成 WARP 密钥失败: %w", err)
	}
	privB64 := base64.StdEncoding.EncodeToString(priv.Bytes())
	pubB64 := base64.StdEncoding.EncodeToString(priv.PublicKey().Bytes())

	body, err := json.Marshal(map[string]any{
		"key":        pubB64,
		"install_id": "",
		"fcm_token":  "",
		"tos":        time.Now().UTC().Format(time.RFC3339),
		"model":      "PC",
		"type":       "Windows",
		"locale":     "en_US",
	})
	if err != nil {
		return Profile{}, err
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/" + c.Version + "/reg"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Profile{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Client-Version", c.CFClient)
	req.Header.Set("User-Agent", c.UserAgent)

	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: defaultRequestTimout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Profile{}, fmt.Errorf("请求 WARP 注册接口失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden {
		return Profile{}, fmt.Errorf("%w: HTTP %d", ErrBlocked, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Profile{}, fmt.Errorf("WARP 注册失败: HTTP %d", resp.StatusCode)
	}
	var r regResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return Profile{}, fmt.Errorf("解析 WARP 注册响应失败: %w", err)
	}
	if r.ID == "" || r.Token == "" || len(r.Config.Peers) == 0 {
		return Profile{}, errors.New("WARP 注册响应缺少必要字段")
	}
	peer := r.Config.Peers[0]
	ipv4 := strings.TrimSpace(r.Config.Interface.Addresses.V4)
	ipv6 := strings.TrimSpace(r.Config.Interface.Addresses.V6)
	if ipv4 == "" && ipv6 == "" {
		return Profile{}, errors.New("WARP 注册响应没有分配客户端地址")
	}
	if peer.PublicKey == "" {
		return Profile{}, errors.New("WARP 注册响应缺少 peer public key")
	}
	return Profile{
		DeviceID:      r.ID,
		Token:         r.Token,
		PrivateKey:    privB64,
		PublicKey:     pubB64,
		PeerPublicKey: peer.PublicKey,
		IPv4:          ipv4,
		IPv6:          ipv6,
		Endpoint:      peer.Endpoint.Host,
	}, nil
}

// Delete 删除 WARP 设备；404 视为已删除。
func (c *Client) Delete(ctx context.Context, deviceID, token string) error {
	if c == nil || strings.TrimSpace(deviceID) == "" || strings.TrimSpace(token) == "" {
		return errors.New("缺少 WARP 设备凭据")
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/" + c.Version + "/reg/" + deviceID
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("CF-Client-Version", c.CFClient)
	req.Header.Set("User-Agent", c.UserAgent)
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: defaultRequestTimout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("删除 WARP 设备失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	return fmt.Errorf("删除 WARP 设备失败: HTTP %d", resp.StatusCode)
}
