package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"proxypilot-agent/internal/client"
)

// clashTotals 节点累计上下行（来自 /connections 的 downloadTotal / uploadTotal）。
type clashTotals struct {
	up   int64
	down int64
}

type clashResponse struct {
	DownloadTotal int64 `json:"downloadTotal"`
	UploadTotal   int64 `json:"uploadTotal"`
	Connections   []struct {
		ID       string `json:"id"`
		Metadata struct {
			Network    string `json:"network"`
			Type       string `json:"type"`
			SourceIP   string `json:"sourceIP"`
			SourcePort string `json:"sourcePort"`
			Host       string `json:"host"`
		} `json:"metadata"`
		Upload   int64  `json:"upload"`
		Download int64  `json:"download"`
		Start    string `json:"start"`
	} `json:"connections"`
}

// clashSnapshot 读一次 /connections（只读 GET；绝不调用 DELETE，避免误断用户连接）。
func clashSnapshot(ctx context.Context, addr, secret string) (clashTotals, []client.ConnInfo, error) {
	var totals clashTotals
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/connections", nil)
	if err != nil {
		return totals, nil, err
	}
	if s := strings.TrimSpace(secret); s != "" {
		req.Header.Set("Authorization", "Bearer "+s)
	}
	cl := &http.Client{Timeout: 4 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return totals, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return totals, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body clashResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return totals, nil, fmt.Errorf("解析失败：%v", err)
	}
	totals.up, totals.down = body.UploadTotal, body.DownloadTotal

	conns := make([]client.ConnInfo, 0, len(body.Connections))
	for _, c := range body.Connections {
		port, _ := strconv.ParseInt(strings.TrimSpace(c.Metadata.SourcePort), 10, 64)
		var startedAt int64
		if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(c.Start)); err == nil {
			startedAt = t.UnixMilli()
		}
		conns = append(conns, client.ConnInfo{
			ID:         c.ID,
			SourceIP:   c.Metadata.SourceIP,
			SourcePort: port,
			Host:       c.Metadata.Host,
			Network:    c.Metadata.Network,
			Inbound:    c.Metadata.Type,
			Upload:     c.Upload,
			Download:   c.Download,
			StartedAt:  startedAt,
		})
	}
	return totals, conns, nil
}
