package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// GET /api/servers/monitor 与 /api/servers/services（别名）
// 返回结构与 Worker 对齐：{ success, servers, services, watch, matrix, summary }
func (h *handlers) serversMonitor(w http.ResponseWriter, r *http.Request) {
	h.geoRefreshAllThrottled(context.Background())

	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM servers ORDER BY id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	watchParam := []string{}
	for _, s := range strings.Split(r.URL.Query().Get("watch"), ",") {
		if s = strings.TrimSpace(s); s != "" && len(watchParam) < 30 {
			watchParam = append(watchParam, s)
		}
	}

	servers := make([]map[string]any, 0, len(rows))
	seen := map[string]bool{}
	var totalRxRate, totalTxRate int64
	for _, row := range rows {
		metrics := map[string]any{}
		if raw := mapStr(row, "last_metrics", ""); raw != "" {
			_ = json.Unmarshal([]byte(raw), &metrics)
		}
		services := map[string]string{}
		if m, ok := metrics["services"].(map[string]any); ok {
			for k, v := range m {
				services[k] = jsonStr(v)
				seen[k] = true
			}
		}
		lastSeen := mapInt(row, "agent_last_seen", 0)
		if lastSeen <= 0 {
			lastSeen = mapInt(row, "last_check_at", 0)
		}
		status := agentOnlineStatus(lastSeen, mapStr(row, "last_status", "unknown"), mapInt(row, "agent_next_poll_sec", 60))
		netMap := jsonMap(metrics["net"])
		totalRxRate += jsonInt(netMap["rxRate"])
		totalTxRate += jsonInt(netMap["txRate"])
		var geoLat, geoLon any
		if v := jsonNum(row["ip_lat"]); v != 0 {
			geoLat = v
		}
		if v := jsonNum(row["ip_lon"]); v != 0 {
			geoLon = v
		}
		servers = append(servers, map[string]any{
			"id": mapInt(row, "id", 0), "name": mapStr(row, "name", ""),
			"status": status, "last_status": status,
			"ip_lat": geoLat, "ip_lon": geoLon,
			"last_error": mapStr(row, "last_error", ""),
			"last_ip":    mapStr(row, "last_ip", ""),
			"last_ip4":   publicDisplayIPv4(mapStr(row, "last_ip4", ""), mapStr(row, "last_ip", "")),
			"ip_country": mapStr(row, "ip_country", ""), "ip_region": mapStr(row, "ip_region", ""),
			"last_check_at":   mapInt(row, "last_check_at", 0),
			"last_latency_ms": mapInt(row, "last_latency_ms", 0),
			"last_seen":       lastSeen,
			"hostname":        mapStr(metrics, "hostname", ""),
			"uptime_sec":      jsonInt(metrics["uptimeSec"]),
			"os":              mapStr(metrics, "os", ""),
			"kernel":          mapStr(metrics, "kernel", ""),
			"agent_platform":  mapStr(row, "agent_platform", ""),
			"agent_version":   mapStr(row, "agent_version", ""),
			"link_mode":       mapStr(row, "link_mode", "agent"),
			"load1":           metrics["load1"], "load5": metrics["load5"], "load15": metrics["load15"],
			"cpu_percent": metrics["cpuPercent"], "cores": metrics["cores"],
			"mem_percent": metrics["memPercent"], "mem_used_mb": metrics["memUsedMb"],
			"mem_total_mb": metrics["memTotalMb"], "disk_percent": metrics["diskPercent"],
			"net": metrics["net"], "disk_io": metrics["diskIo"], "conn": metrics["conn"],
			"proc": metrics["proc"], "services": services, "ssh": metrics["ssh"],
			"metrics": metrics,
		})
	}

	// 关注列表：显式参数优先；否则从模板/配置的 service_name + 实际上报服务汇总
	watch := append([]string{}, watchParam...)
	if len(watch) == 0 {
		names := map[string]bool{}
		for _, q := range []string{
			`SELECT service_name FROM node_templates WHERE enabled = 1`,
			`SELECT service_name FROM node_configs WHERE enabled = 1`,
		} {
			if rs, err := queryRowsAsMaps(r.Context(), h.db, q); err == nil {
				for _, it := range rs {
					for _, n := range strings.Split(mapStr(it, "service_name", ""), ",") {
						if n = strings.TrimSpace(n); n != "" {
							names[n] = true
						}
					}
				}
			}
		}
		for n := range seen {
			names[n] = true
		}
		for n := range names {
			if len(watch) >= 30 {
				break
			}
			watch = append(watch, n)
		}
	}

	stateOf := func(srv map[string]any, name string) string {
		if svc, ok := srv["services"].(map[string]string); ok {
			if v, ok := svc[name]; ok {
				return v
			}
		}
		if mapStr(srv, "status", "") == "online" {
			return "unknown"
		}
		return "offline"
	}
	matrix := make([]map[string]any, 0, len(watch))
	for _, name := range watch {
		states := make([]map[string]any, 0, len(servers))
		for _, srv := range servers {
			states = append(states, map[string]any{
				"server_id": mapInt(srv, "id", 0), "server_name": mapStr(srv, "name", ""),
				"status": mapStr(srv, "status", ""), "state": stateOf(srv, name),
			})
		}
		matrix = append(matrix, map[string]any{"service": name, "states": states})
	}

	failed, failedList, notInstalled := 0, []map[string]any{}, 0
	for _, srv := range servers {
		// 服务器整体离线时只保留离线告警，不再重复提示其服务状态。
		if mapStr(srv, "status", "") == "offline" {
			continue
		}
		svc, _ := srv["services"].(map[string]string)
		for name, st := range svc {
			switch st {
			case "not-installed":
				notInstalled++
			case "active", "inactive":
			default:
				failed++
				if len(failedList) < 20 {
					failedList = append(failedList, map[string]any{
						"server_id": mapInt(srv, "id", 0), "server_name": mapStr(srv, "name", ""),
						"service": name, "state": st,
					})
				}
			}
		}
	}

	online, offline := 0, 0
	for _, srv := range servers {
		switch mapStr(srv, "status", "") {
		case "online":
			online++
		case "offline":
			offline++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "servers": servers, "services": keysOf(seen), "watch": watch, "matrix": matrix,
		"summary": map[string]any{
			"servers": len(servers), "online": online, "offline": offline,
			"failed_services": failed, "failed_list": failedList, "not_installed": notInstalled,
			"rx_rate": totalRxRate, "tx_rate": totalTxRate,
		},
	})
}

func (h *handlers) serversServices(w http.ResponseWriter, r *http.Request) {
	h.serversMonitor(w, r)
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// GET/POST /api/singbox —— 域根（健康检查）
func (h *handlers) singboxRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "service": "singbox", "migrated": true})
}

// GET/POST /api/mtproto —— 域根（健康检查）
func (h *handlers) mtprotoRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "service": "mtproto", "migrated": true})
}
