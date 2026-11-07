// Package stats 采集 sing-box 的统计快照：连接明细（clash_api）与按用户流量（v2ray_api）。
//
// 设计：服务器只下发「要采哪个节点 + sb.json 在哪」，Agent 自己读盘解析 experimental，
// 保证「盘上的配置」就是采集依据，不会因为服务端与节点配置漂移而采错端口。
package stats

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"proxypilot-agent/internal/client"
)

// DefaultSBPath 默认 sing-box 配置路径。
const DefaultSBPath = "/etc/sing-box/sb.json"

// collectTimeout 单节点采集上限（两个控制面共享）。
const collectTimeout = 12 * time.Second

// Experimental 只取统计需要的两段（clash_api / v2ray_api）。
type Experimental struct {
	ClashAPI struct {
		ExternalController string `json:"external_controller"`
		Secret             string `json:"secret"`
	} `json:"clash_api"`
	V2RayAPI struct {
		Listen string `json:"listen"`
	} `json:"v2ray_api"`
}

// ReadExperimental 从 sb.json 读取统计控制面配置。
func ReadExperimental(path string) (Experimental, error) {
	var exp Experimental
	raw, err := os.ReadFile(path)
	if err != nil {
		return exp, fmt.Errorf("读取 %s 失败：%v", path, err)
	}
	var doc struct {
		Experimental *Experimental `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return exp, fmt.Errorf("%s 不是合法 JSON：%v", path, err)
	}
	if doc.Experimental == nil {
		return exp, errors.New("sb.json 未启用统计（缺少 experimental），请重新下发节点")
	}
	return *doc.Experimental, nil
}

// Collect 采集一个节点；单个控制面失败不算整体失败，错误会并进 Error。
func Collect(target client.StatsTarget) client.NodeStats {
	out := client.NodeStats{NodeID: target.NodeID}
	path := strings.TrimSpace(target.SBPath)
	if path == "" {
		path = DefaultSBPath
	}
	if abs, aerr := filepath.Abs(filepath.Clean(path)); aerr != nil || !sbPathAllowed(abs) {
		out.Error = "sb.json 路径不合法"
		return out
	} else {
		path = abs
	}
	exp, err := ReadExperimental(path)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	ctx, cancel := context.WithTimeout(context.Background(), collectTimeout)
	defer cancel()

	var errs []string
	sampled := false
	if addr := strings.TrimSpace(exp.ClashAPI.ExternalController); addr != "" {
		totals, conns, err := clashSnapshot(ctx, addr, exp.ClashAPI.Secret)
		if err != nil {
			errs = append(errs, "clash_api: "+err.Error())
		} else {
			out.TotalUp, out.TotalDown = totals.up, totals.down
			out.Connections = conns
			sampled = true
		}
	}
	if addr := strings.TrimSpace(exp.V2RayAPI.Listen); addr != "" {
		users, err := v2rayUserStats(ctx, addr)
		if err != nil {
			errs = append(errs, "v2ray_api: "+err.Error())
		} else {
			out.Users = users
			sampled = true
		}
	}
	if !sampled && len(errs) == 0 {
		out.Error = "sb.json 里没有 clash_api / v2ray_api"
		return out
	}
	out.OK = sampled
	if len(errs) > 0 {
		out.Error = strings.Join(errs, "; ")
	}
	return out
}

// CollectAll 串行采集一组目标（单节点失败不影响其它节点）。
func CollectAll(targets []client.StatsTarget) []client.NodeStats {
	out := make([]client.NodeStats, 0, len(targets))
	for _, t := range targets {
		out = append(out, Collect(t))
	}
	return out
}

func sbPathAllowed(abs string) bool {
	for _, root := range []string{"/etc/sing-box/", "/etc/mihomo/"} {
		if strings.HasPrefix(abs, root) {
			return true
		}
	}
	return false
}
