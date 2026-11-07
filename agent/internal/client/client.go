// Package client 实现与 Worker 的 agent wire 协议（POST /api/agent/poll、/api/agent/ack）。
//
// 协议与 V8.1 shell agent 完全一致（服务端零改动）：
//   - 请求头 X-Agent-Token: ag_xxx、User-Agent: ProxyPilot-Agent/<版本>；
//   - 任务以 wire 字段下发：记录用 ~ 分隔，字段用 | 分隔，文本字段为 base64；
//   - 字段顺序：id|type|config_id|name|path|content|sha256|backup|restart_cmd|check_cmd|service_name|version|command|timeout
package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"proxypilot-agent/internal/metrics"
	"strconv"
	"strings"
	"time"
)

const (
	// MaxOutputBytes ack 回传输出上限（与 shell agent 的 head -c 1200 一致）。
	MaxOutputBytes = 16384
	// PollTimeout / AckTimeout 单次请求超时（与 shell agent 的 curl -m 30 / -m 20 一致）。
	PollTimeout = 30 * time.Second
	AckTimeout  = 20 * time.Second
)

// ErrWaitUnsupported Worker 没启用长轮询（旧版本 / 未绑定 AGENT_HUB）。
var ErrWaitUnsupported = errors.New("长轮询不受支持")

// Applied 已应用版本（每轮上报，服务端据此派生版本差异下发）。
type Applied struct {
	ConfigID int `json:"config_id"`
	Version  int `json:"version"`
}

// PollRequest 与 Worker handleAgentPoll 读取的字段一一对应。
type PollRequest struct {
	AgentVersion string            `json:"agent_version"`
	Hostname     string            `json:"hostname"`
	OS           string            `json:"os"`
	Kernel       string            `json:"kernel"`
	Platform     string            `json:"platform"`
	Arch         string            `json:"arch"`
	Cores        int               `json:"cores"`
	UptimeSec    int64             `json:"uptime_sec"`
	Load         string            `json:"load"`
	MemUsedMB    int64             `json:"mem_used_mb"`
	MemTotalMB   int64             `json:"mem_total_mb"`
	DiskPercent  float64           `json:"disk_percent"`
	CPUTicks     int64             `json:"cpu_ticks"`
	Services     map[string]string `json:"services"`
	Versions     map[string]string `json:"versions,omitempty"`
	SSH          metrics.SSHState  `json:"ssh"` // V13.5：组件版本（sing-box / xray / mtg-multi）
	TCPPorts     string            `json:"tcp_ports"`
	IP           string            `json:"ip"`
	PublicIP     string            `json:"public_ip,omitempty"`
	PrivateIP    string            `json:"private_ip,omitempty"`
	MeshIP4      string            `json:"mesh_ip4,omitempty"`
	MeshIP6      string            `json:"mesh_ip6,omitempty"`
	IPv6         string            `json:"ipv6,omitempty"`

	// V11.0 服务器监控字段（之前采了但没随 poll 上报，导致后台「连接 / 进程 / 网络 / 磁盘 IO / 温度」恒为 0）
	Net    metrics.NetStats   `json:"net"`
	DiskIO metrics.DiskIO     `json:"disk_io"`
	Conns  metrics.ConnStates `json:"conn"`
	Procs  metrics.ProcInfo   `json:"proc"`

	Applied []Applied `json:"applied"`

	// V15.6：sing-box 统计快照（上一轮采集的结果，随本轮 poll 上报）
	Stats []NodeStats `json:"stats,omitempty"`
}

// PollResponse 服务端响应（只取用得到的字段）。
type PollResponse struct {
	OK                 bool       `json:"ok"`
	ServerID           int        `json:"server_id"`
	ServerName         string     `json:"server_name"`
	NextPollSec        int        `json:"next_poll_sec"`
	MetricsEvery       int        `json:"metrics_every"`
	ServerTime         int64      `json:"server_time"`
	AgentAccessURL     string     `json:"agent_access_url"`
	AgentScriptVersion string     `json:"agent_script_version"`
	Wire               string     `json:"wire"`
	Shell              *ShellHint `json:"shell,omitempty"`
	Error              string     `json:"error"`

	// V15.6：本机需要采集统计的节点（Agent 自己读 sb.json 解析 experimental）
	StatsTargets []StatsTarget `json:"stats_targets,omitempty"`

	// V15.6：组件分发基址（自建 sing-box 等放在 <base>/sing-box/ 下）
	AgentBinaryBase string `json:"agent_binary_base,omitempty"`
	// V15.6：组件（sing-box）独立分发基址，桶里单独一个目录
	ComponentBase string `json:"component_base,omitempty"`
}

// StatsTarget 一个统计采集目标（一台服务器可能绑定多个节点）。
type StatsTarget struct {
	NodeID int64  `json:"node_id"`
	SBPath string `json:"sb_path,omitempty"`
}

// UserTraffic 单个用户的累计上下行（来自 sing-box v2ray_api 的 user>>> 计数器）。
type UserTraffic struct {
	Name string `json:"name"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// ConnInfo 单条连接（来自 sing-box clash_api 的 /connections）。
type ConnInfo struct {
	ID         string `json:"id"`
	SourceIP   string `json:"source_ip"`
	SourcePort int64  `json:"source_port"`
	Host       string `json:"host"`
	Network    string `json:"network"`
	Inbound    string `json:"inbound"`
	User       string `json:"user"`
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
	StartedAt  int64  `json:"started_at"`
}

// NodeStats 一个节点的一次统计快照。
type NodeStats struct {
	NodeID      int64         `json:"node_id"`
	OK          bool          `json:"ok"`
	Error       string        `json:"error,omitempty"`
	TotalUp     int64         `json:"total_up"`
	TotalDown   int64         `json:"total_down"`
	Users       []UserTraffic `json:"users,omitempty"`
	Connections []ConnInfo    `json:"connections,omitempty"`
}

// ShellHint 表示后台有人打开了实时终端，Agent 应主动拨号接入。
type ShellHint struct {
	Session  string `json:"session"`
	MinAgent string `json:"min_agent,omitempty"`
}

// Action 是解析后的任务。
type Action struct {
	ID          int
	Type        string
	ConfigID    int
	Name        string
	Path        string
	Content     string
	Sha256      string
	Backup      bool
	RestartCmd  string
	CheckCmd    string
	ServiceName string
	Version     int
	Command     string
	Timeout     int
	// V13.7：模板要求的引擎版本，如 {"sing-box":"1.15.0-alpha.13"}；空 = 用内置默认
	InstallVersions map[string]string
	// DecodeErr 任一 base64 文本字段解码失败（deploy 必须因此失败，绝不能写入空文件）
	DecodeErr bool
}

// Result 单条任务回执。
type Result struct {
	ID        int    `json:"id"`
	Status    string `json:"status"`
	ExitCode  int    `json:"exit_code"`
	OutputB64 string `json:"output_b64"`
}

// AckRequest 回执请求体。
type AckRequest struct {
	AgentVersion string   `json:"agent_version"`
	Results      []Result `json:"results"`
}

// StatusError 带 HTTP 状态码的错误（便于区分 401/403/429 并读取 Retry-After）。
type StatusError struct {
	Status     int
	Body       string
	RetryAfter int
}

func (e *StatusError) Error() string {
	msg := fmt.Sprintf("HTTP %d", e.Status)
	if e.Status == 429 && e.RetryAfter > 0 {
		msg += fmt.Sprintf("（轮询过于频繁，建议 %ds 后重试）", e.RetryAfter)
	}
	body := strings.TrimSpace(e.Body)
	if body != "" {
		if len(body) > 200 {
			body = body[:200]
		}
		msg += "：" + body
	}
	return msg
}

// Client 是与 Worker 通信的 HTTP 客户端。
type Client struct {
	Origin  string
	Key     string
	Version string
	HTTP    *http.Client
}

// New 构造客户端；insecure 为真时跳过 TLS 证书校验（仅排查用）。
// SetOrigin 切换基址（内网/公网切换时复用同一 Client）。
func (c *Client) SetOrigin(origin string) {
	c.Origin = strings.TrimRight(strings.TrimSpace(origin), "/")
}

func New(origin, key, version string, insecure bool) *Client {
	transport := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     60 * time.Second,
	}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{
		Origin:  strings.TrimRight(origin, "/"),
		Key:     key,
		Version: version,
		HTTP:    &http.Client{Transport: transport},
	}
}

// Poll 上报心跳/指标并领取任务。
func (c *Client) Poll(ctx context.Context, req PollRequest) (PollResponse, error) {
	var resp PollResponse
	body, err := c.post(ctx, "/api/agent/poll", req, &resp)
	if err != nil {
		return resp, err
	}
	_ = body
	return resp, nil
}

// Wait 长轮询等待任务：200=有任务被唤醒，204=超时，404=服务端不支持。
func (c *Client) Wait(ctx context.Context, timeoutSec int) (bool, error) {
	if timeoutSec <= 0 {
		timeoutSec = 20
	}
	url := fmt.Sprintf("%s/api/agent/wait?timeout=%d", c.Origin, timeoutSec)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("构造长轮询请求失败：%v", err)
	}
	req.Header.Set("X-Agent-Token", c.Key)
	req.Header.Set("User-Agent", "ProxyPilot-Agent/"+c.Version)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("长轮询请求失败：%v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNoContent:
		return false, nil
	case http.StatusNotFound:
		return false, ErrWaitUnsupported
	default:
		return false, fmt.Errorf("长轮询返回 HTTP %d", resp.StatusCode)
	}
}

// Ack 回传任务执行结果。
func (c *Client) Ack(ctx context.Context, req AckRequest) error {
	_, err := c.post(ctx, "/api/agent/ack", req, nil)
	return err
}

// WSURL Agent WebSocket 地址（http(s) → ws(s)）。
func (c *Client) WSURL() string {
	origin := c.Origin
	switch {
	case strings.HasPrefix(origin, "https://"):
		origin = "wss://" + strings.TrimPrefix(origin, "https://")
	case strings.HasPrefix(origin, "http://"):
		origin = "ws://" + strings.TrimPrefix(origin, "http://")
	}
	return origin + "/api/agent/ws"
}

func (c *Client) post(ctx context.Context, path string, payload interface{}, out interface{}) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败：%v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Origin+path, bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败：%v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", c.Key)
	req.Header.Set("User-Agent", "ProxyPilot-Agent/"+c.Version)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败：%v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Status: resp.StatusCode, Body: string(data), RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return data, fmt.Errorf("解析响应失败：%v", err)
		}
	}
	return data, nil
}

func parseRetryAfter(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// EncodeOutput 把任务输出截断并 base64（与 shell agent 的 head -c 1200 | base64 一致）。
func EncodeOutput(text string) string {
	b := []byte(text)
	if len(b) > MaxOutputBytes {
		b = b[:MaxOutputBytes]
	}
	return base64.StdEncoding.EncodeToString(b)
}
