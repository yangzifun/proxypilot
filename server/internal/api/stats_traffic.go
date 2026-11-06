package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// =====================================================================
//  sing-box 统计：节点侧控制面密钥、Agent 上报入库、查询接口（V15.6）
//
//  数据链路：
//    sb.json experimental.clash_api / v2ray_api（只绑 127.0.0.1）
//      → Agent 每轮 poll 读取快照
//      → 随下一次 poll 上报
//      → 本文件差分入库（user_traffic / user_traffic_hourly / node_connections）
// =====================================================================

// statsActiveWindowMs 「活跃」判定窗口：10 分钟（采集间隔约 36s，容忍空闲与抖动）
const statsActiveWindowMs int64 = 600_000

var regexpUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// randomStatsSecret 生成统计控制面的 secret（32 hex）。
func randomStatsSecret() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// ensureNodeStatsSecret 读取节点统计 secret；没有就生成并加密落库。
// 加密复用接入密钥那套 AES-GCM（enc:v1:...）。
func (h *handlers) ensureNodeStatsSecret(ctx context.Context, nodeID int64) string {
	if nodeID <= 0 {
		return ""
	}
	var enc string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(stats_secret_enc,'') FROM singbox_nodes WHERE id = ?`, nodeID).Scan(&enc); err != nil {
		return ""
	}
	if s := decryptAgentToken(h.agentKeySecret(), enc); s != "" {
		return s
	}
	secret := randomStatsSecret()
	if secret == "" {
		return ""
	}
	encNew, err := encryptAgentToken(h.agentKeySecret(), secret)
	if err != nil {
		return ""
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE singbox_nodes SET stats_secret_enc = ?, updated_at = ? WHERE id = ?`, encNew, nowMillis(), nodeID); err != nil {
		return ""
	}
	return secret
}

// nodeStatsEnabled 节点是否启用统计（默认开）。
func nodeStatsEnabled(row map[string]any) bool {
	if v, ok := row["stats_enabled"]; ok && v != nil {
		return jsonInt(v) != 0
	}
	return true
}

// ---------------- 入库 ----------------

// agentIngestStats 处理 poll 请求里的 stats 数组（容错：任何字段缺失都跳过）。
func (h *handlers) agentIngestStats(ctx context.Context, serverID int64, raw any) {
	items := jsonSlice(raw)
	if len(items) == 0 {
		return
	}
	now := nowMillis()
	for _, item := range items {
		m := jsonMap(item)
		nodeID := jsonInt(m["node_id"])
		if nodeID <= 0 {
			continue
		}
		var owner int64
		if err := h.db.QueryRowContext(ctx,
			`SELECT server_id FROM singbox_nodes WHERE id = ?`, nodeID).Scan(&owner); err != nil || owner != serverID {
			continue
		}
		ok, _ := m["ok"].(bool)
		errText := truncateRunes(jsonStr(m["error"]), 500)
		if !ok {
			_, _ = h.db.ExecContext(ctx, `INSERT INTO node_stats_snapshot
			      (node_id, sampled_at, conn_count, up_total, down_total, up_rate, down_rate, err_text)
			      VALUES (?, ?, 0, 0, 0, 0, 0, ?)
			      ON DUPLICATE KEY UPDATE sampled_at = VALUES(sampled_at), conn_count = 0,
			      up_rate = 0, down_rate = 0, err_text = VALUES(err_text)`,
				nodeID, now, errText)
			continue
		}

		h.ingestUserCounters(ctx, nodeID, jsonSlice(m["users"]), now)
		h.ingestConnections(ctx, nodeID, jsonSlice(m["connections"]), now)
		h.ingestSnapshot(ctx, nodeID, jsonInt(m["total_up"]), jsonInt(m["total_down"]), int64(len(jsonSlice(m["connections"]))), now)
	}
}

func (h *handlers) ingestUserCounters(ctx context.Context, nodeID int64, users []any, now int64) {
	hourTS := now - now%3_600_000
	for _, raw := range users {
		u := jsonMap(raw)
		uuid := strings.TrimSpace(jsonStr(u["name"]))
		if !regexpUUID.MatchString(uuid) {
			continue // 非 UUID 用户名（未开统计的老配置）无法对应到平台用户
		}
		up := jsonInt(u["up"])
		down := jsonInt(u["down"])
		if up < 0 || down < 0 {
			continue
		}

		var prevUp, prevDown int64
		hasPrev := false
		if err := h.db.QueryRowContext(ctx,
			`SELECT up_total, down_total FROM node_user_counter WHERE node_id = ? AND uuid = ?`,
			nodeID, uuid).Scan(&prevUp, &prevDown); err == nil {
			hasPrev = true
		}
		dUp, dDown := up, down
		if hasPrev {
			if up >= prevUp {
				dUp = up - prevUp
			}
			if down >= prevDown {
				dDown = down - prevDown
			}
		}
		if _, err := h.db.ExecContext(ctx, `INSERT INTO node_user_counter
		      (node_id, uuid, up_total, down_total, sampled_at) VALUES (?, ?, ?, ?, ?)
		      ON DUPLICATE KEY UPDATE up_total = VALUES(up_total), down_total = VALUES(down_total),
		      sampled_at = VALUES(sampled_at)`,
			nodeID, uuid, up, down, now); err != nil {
			h.log.Warn("写入用户计数基线失败", "err", err, "uuid", uuid)
			continue
		}
		if dUp <= 0 && dDown <= 0 {
			continue
		}
		if _, err := h.db.ExecContext(ctx, `INSERT INTO user_traffic_hourly
		      (uuid, node_id, hour_ts, up, down) VALUES (?, ?, ?, ?, ?)
		      ON DUPLICATE KEY UPDATE up = up + VALUES(up), down = down + VALUES(down)`,
			uuid, nodeID, hourTS, dUp, dDown); err != nil {
			h.log.Warn("写入用户小时流量失败", "err", err, "uuid", uuid)
			continue
		}
		if _, err := h.db.ExecContext(ctx, `INSERT INTO user_traffic
		      (uuid, up_total, down_total, last_active_at, updated_at) VALUES (?, ?, ?, ?, ?)
		      ON DUPLICATE KEY UPDATE up_total = up_total + VALUES(up_total),
		      down_total = down_total + VALUES(down_total),
		      last_active_at = GREATEST(last_active_at, VALUES(last_active_at)),
		      updated_at = VALUES(updated_at)`,
			uuid, dUp, dDown, now, now); err != nil {
			h.log.Warn("写入用户累计流量失败", "err", err, "uuid", uuid)
		}
	}
}

func (h *handlers) ingestConnections(ctx context.Context, nodeID int64, conns []any, now int64) {
	// 先清掉 3 个采样周期内没再出现的连接，避免残留
	_, _ = h.db.ExecContext(ctx, `DELETE FROM node_connections WHERE node_id = ? AND seen_at < ?`,
		nodeID, now-3*statsActiveWindowMs)
	if len(conns) > 2000 {
		conns = conns[:2000]
	}
	onlineUUIDs := map[string]bool{}
	for _, raw := range conns {
		c := jsonMap(raw)
		id := strings.TrimSpace(jsonStr(c["id"]))
		if id == "" {
			continue
		}
		meta := jsonMap(c["metadata"])
		if u := jsonStr(meta["user"]); u != "" && regexpUUID.MatchString(u) {
			onlineUUIDs[u] = true
		}
		srcIP := jsonStr(meta["sourceIP"])
		if srcIP == "" {
			srcIP = jsonStr(c["source_ip"])
		}
		host := jsonStr(meta["host"])
		if host == "" {
			host = jsonStr(c["host"])
		}
		network := jsonStr(meta["network"])
		if network == "" {
			network = jsonStr(c["network"])
		}
		inbound := jsonStr(meta["type"])
		if inbound == "" {
			inbound = jsonStr(c["inbound"])
		}
		srcPort := jsonInt(meta["sourcePort"])
		if srcPort == 0 {
			srcPort = jsonInt(c["source_port"])
		}
		if _, err := h.db.ExecContext(ctx, `INSERT INTO node_connections
		      (node_id, conn_id, source_ip, source_port, host, network, inbound, upload, download, started_at, seen_at)
		      VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		      ON DUPLICATE KEY UPDATE source_ip = VALUES(source_ip), source_port = VALUES(source_port),
		      host = VALUES(host), network = VALUES(network), inbound = VALUES(inbound),
		      upload = VALUES(upload), download = VALUES(download), seen_at = VALUES(seen_at)`,
			nodeID, truncateRunes(id, 64), truncateRunes(srcIP, 45), srcPort, truncateRunes(host, 255),
			truncateRunes(network, 16), truncateRunes(inbound, 64),
			jsonInt(c["upload"]), jsonInt(c["download"]), jsonInt(c["start"]), now); err != nil {
			h.log.Warn("写入连接快照失败", "err", err)
			return
		}
	}
	for u := range onlineUUIDs {
		_, _ = h.db.ExecContext(ctx, `INSERT INTO user_traffic (uuid, up_total, down_total, last_active_at, updated_at)
			VALUES (?, 0, 0, ?, ?)
			ON DUPLICATE KEY UPDATE last_active_at = GREATEST(last_active_at, VALUES(last_active_at)), updated_at = VALUES(updated_at)`,
			u, now, now)
	}
}

func (h *handlers) ingestSnapshot(ctx context.Context, nodeID, upTotal, downTotal, connCount, now int64) {
	var (
		prevAt           int64
		prevUp, prevDown int64
	)
	hasPrev := false
	if err := h.db.QueryRowContext(ctx, `SELECT sampled_at, up_total, down_total FROM node_stats_snapshot WHERE node_id = ?`,
		nodeID).Scan(&prevAt, &prevUp, &prevDown); err == nil {
		hasPrev = true
	}
	var upRate, downRate int64
	if hasPrev && now > prevAt {
		secs := (now - prevAt) / 1000
		if secs >= 1 {
			if upTotal >= prevUp {
				upRate = (upTotal - prevUp) / secs
			}
			if downTotal >= prevDown {
				downRate = (downTotal - prevDown) / secs
			}
		}
	}
	if _, err := h.db.ExecContext(ctx, `INSERT INTO node_stats_snapshot
	      (node_id, sampled_at, conn_count, up_total, down_total, up_rate, down_rate, err_text)
	      VALUES (?, ?, ?, ?, ?, ?, ?, '')
	      ON DUPLICATE KEY UPDATE sampled_at = VALUES(sampled_at), conn_count = VALUES(conn_count),
	      up_total = VALUES(up_total), down_total = VALUES(down_total),
	      up_rate = VALUES(up_rate), down_rate = VALUES(down_rate), err_text = ''`,
		nodeID, now, connCount, upTotal, downTotal, upRate, downRate); err != nil {
		h.log.Warn("写入节点统计快照失败", "err", err)
	}
}

// ---------------- 查询接口 ----------------

// statsRangeStart 解析 range=24h|7d|30d|all，返回起始毫秒时间戳（0 = 不限制）。
func statsRangeStart(raw string) int64 {
	now := nowMillis()
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "24h", "1d":
		return now - 24*3_600_000
	case "30d":
		return now - 30*24*3_600_000
	case "all":
		return 0
	default: // 7d
		return now - 7*24*3_600_000
	}
}

// GET /api/stats/traffic/users?range=7d
func (h *handlers) statsTrafficUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	start := statsRangeStart(r.URL.Query().Get("range"))
	now := nowMillis()
	todayStart := now - now%86_400_000 // 当天 00:00 UTC

	query := `SELECT ut.uuid,
	      COALESCE(SUM(ut.up),0) AS up, COALESCE(SUM(ut.down),0) AS down,
	      COALESCE(SUM(CASE WHEN ut.hour_ts >= ? THEN ut.up ELSE 0 END),0) AS up_today,
	      COALESCE(SUM(CASE WHEN ut.hour_ts >= ? THEN ut.down ELSE 0 END),0) AS down_today,
	      COALESCE(ui.owner,'') AS owner, COALESCE(ui.email,'') AS email,
	      COALESCE(tr.last_active_at,0) AS last_active_at,
	      COALESCE(tr.up_total,0) AS up_total, COALESCE(tr.down_total,0) AS down_total
	    FROM user_traffic_hourly ut
	    LEFT JOIN uuid_info ui ON ui.uuid = ut.uuid
	    LEFT JOIN user_traffic tr ON tr.uuid = ut.uuid`
	args := []any{todayStart, todayStart}
	if start > 0 {
		query += ` WHERE ut.hour_ts >= ?`
		args = append(args, start)
	}
	query += ` GROUP BY ut.uuid, ui.owner, ui.email, tr.last_active_at, tr.up_total, tr.down_total
	    ORDER BY (SUM(ut.up) + SUM(ut.down)) DESC LIMIT 500`

	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询流量失败")
		return
	}
	defer rows.Close()

	data := []map[string]any{}
	for rows.Next() {
		var (
			uuid, owner, email         string
			up, down, upToday, dnToday int64
			lastAt                     int64
			upTotal, downTotal         int64
		)
		if rows.Scan(&uuid, &up, &down, &upToday, &dnToday, &owner, &email, &lastAt, &upTotal, &downTotal) != nil {
			continue
		}
		data = append(data, map[string]any{
			"uuid": uuid, "owner": owner, "email": email,
			"up": up, "down": down, "total": up + down,
			"up_today": upToday, "down_today": dnToday, "total_today": upToday + dnToday,
			"up_total": upTotal, "down_total": downTotal, "total_all": upTotal + downTotal,
			"last_active_at": lastAt,
			"active":         lastAt > 0 && now-lastAt <= statsActiveWindowMs,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}

// GET /api/stats/traffic/users/{uuid}?range=7d
func (h *handlers) statsTrafficUserDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if !regexpUUID.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID 格式无效")
		return
	}
	q := r.URL.Query()
	start := statsRangeStart(q.Get("range"))
	end := int64(0)
	if v, err := strconv.ParseInt(strings.TrimSpace(q.Get("from")), 10, 64); err == nil && v > 0 {
		start = v
	}
	if v, err := strconv.ParseInt(strings.TrimSpace(q.Get("to")), 10, 64); err == nil && v > 0 {
		end = v
	}
	if end > 0 && start >= end {
		start = end - 7*24*3_600_000
	}
	query := `SELECT hour_ts, COALESCE(SUM(up),0), COALESCE(SUM(down),0)
	    FROM user_traffic_hourly WHERE uuid = ?`
	args := []any{uuid}
	if start > 0 {
		query += ` AND hour_ts >= ?`
		args = append(args, start)
	}
	if end > 0 {
		query += ` AND hour_ts <= ?`
		args = append(args, end)
	}
	query += ` GROUP BY hour_ts ORDER BY hour_ts ASC LIMIT 2000`
	rows, err := h.db.QueryContext(ctx, query, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	defer rows.Close()
	series := []map[string]any{}
	var sumUp, sumDown int64
	for rows.Next() {
		var ts, up, down int64
		if rows.Scan(&ts, &up, &down) != nil {
			continue
		}
		sumUp += up
		sumDown += down
		series = append(series, map[string]any{"hour_ts": ts, "up": up, "down": down})
	}
	var owner, email string
	var lastActive int64
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(owner,''), COALESCE(email,'') FROM uuid_info WHERE uuid = ? LIMIT 1`,
		uuid).Scan(&owner, &email)
	_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(last_active_at,0) FROM user_traffic WHERE uuid = ? LIMIT 1`,
		uuid).Scan(&lastActive)

	// 按节点的用量明细（页面「按节点」表）
	nodeQuery := `SELECT h.node_id, COALESCE(n.name,''), COALESCE(SUM(h.up),0), COALESCE(SUM(h.down),0)
	    FROM user_traffic_hourly h LEFT JOIN singbox_nodes n ON n.id = h.node_id
	    WHERE h.uuid = ?`
	nodeArgs := []any{uuid}
	if start > 0 {
		nodeQuery += ` AND h.hour_ts >= ?`
		nodeArgs = append(nodeArgs, start)
	}
	if end > 0 {
		nodeQuery += ` AND h.hour_ts <= ?`
		nodeArgs = append(nodeArgs, end)
	}
	nodeQuery += ` GROUP BY h.node_id, n.name ORDER BY SUM(h.down) DESC`
	nodes := []map[string]any{}
	if nrows, err := h.db.QueryContext(ctx, nodeQuery, nodeArgs...); err == nil {
		defer nrows.Close()
		for nrows.Next() {
			var nodeID, nUp, nDown int64
			var nodeName string
			if nrows.Scan(&nodeID, &nodeName, &nUp, &nDown) == nil {
				nodes = append(nodes, map[string]any{
					"node_id": nodeID, "node_name": nodeName,
					"up": nUp, "down": nDown, "total": nUp + nDown,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "uuid": uuid, "owner": owner, "email": email,
		"up": sumUp, "down": sumDown, "last_active_at": lastActive,
		"active": lastActive > 0 && nowMillis()-lastActive <= statsActiveWindowMs,
		"series": series, "nodes": nodes, "start": start, "end": end,
	})
}

// GET /api/stats/traffic/nodes
func (h *handlers) statsTrafficNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.id AS node_id, n.name AS node_name,
	      n.server_id, COALESCE(s.name,'') AS server_name, n.stats_enabled,
	      COALESCE(snap.sampled_at,0) AS sampled_at, COALESCE(snap.conn_count,0) AS conn_count,
	      COALESCE(snap.up_total,0) AS up_total, COALESCE(snap.down_total,0) AS down_total,
	      COALESCE(snap.up_rate,0) AS up_rate, COALESCE(snap.down_rate,0) AS down_rate,
	      COALESCE(snap.err_text,'') AS stats_error
	    FROM singbox_nodes n
	    LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN node_stats_snapshot snap ON snap.node_id = n.id
	    ORDER BY n.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点统计失败")
		return
	}
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		data = append(data, map[string]any{
			"node_id": mapInt(row, "node_id", 0), "node_name": mapStr(row, "node_name", ""),
			"server_id":     mapInt(row, "server_id", 0),
			"server_name":   mapStr(row, "server_name", ""),
			"stats_enabled": mapInt(row, "stats_enabled", 1) != 0,
			"sampled_at":    mapInt(row, "sampled_at", 0),
			"conn_count":    mapInt(row, "conn_count", 0),
			"up_total":      mapInt(row, "up_total", 0), "down_total": mapInt(row, "down_total", 0),
			"up_rate": mapInt(row, "up_rate", 0), "down_rate": mapInt(row, "down_rate", 0),
			"error": mapStr(row, "stats_error", ""),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data})
}

// GET /api/nodes/{id}/connections
func (h *handlers) nodeConnections(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "节点不存在")
		return
	}
	rows, err := h.db.QueryContext(r.Context(), `SELECT COALESCE(source_ip,''), source_port, COALESCE(host,''),
	      COALESCE(network,''), COALESCE(inbound,''), upload, download, started_at, seen_at
	    FROM node_connections WHERE node_id = ? ORDER BY download DESC LIMIT 500`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询连接失败")
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var (
			srcIP, host, network, inbound string
			srcPort, upload, download     int64
			startedAt, seenAt             int64
		)
		if rows.Scan(&srcIP, &srcPort, &host, &network, &inbound, &upload, &download, &startedAt, &seenAt) != nil {
			continue
		}
		data = append(data, map[string]any{
			"source_ip": srcIP, "source_port": srcPort, "host": host,
			"network": network, "inbound": inbound,
			"upload": upload, "download": download,
			"started_at": startedAt, "seen_at": seenAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": len(data), "data": data,
		"node_id": id})
}

// 保留期清理：小时聚合按天清理（cron 调用）。
func (h *handlers) pruneUserTraffic(ctx context.Context, keepDays int64) {
	if keepDays <= 0 {
		keepDays = 90
	}
	cutoff := nowMillis() - keepDays*24*3_600_000
	if _, err := h.db.ExecContext(ctx, `DELETE FROM user_traffic_hourly WHERE hour_ts < ?`, cutoff); err != nil {
		h.log.Warn("清理用户流量历史失败", "err", err)
	}
	if _, err := h.db.ExecContext(ctx, `DELETE FROM node_connections WHERE seen_at < ?`,
		nowMillis()-24*3_600_000); err != nil {
		h.log.Warn("清理连接快照失败", "err", err)
	}
}

// POST /api/stats/traffic/clear  {uuid?}
// 清理流量统计记录：带 uuid 只清该用户；不带则清空全部用户流量与连接记录。
func (h *handlers) statsTrafficClear(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UUID string `json:"uuid"`
	}
	_ = decodeJSON(w, r, &body)
	uuid := strings.ToLower(strings.TrimSpace(body.UUID))
	ctx := r.Context()
	counts := map[string]int64{}
	del := func(table, where string, args ...any) {
		res, err := h.db.ExecContext(ctx, `DELETE FROM `+table+` WHERE `+where, args...)
		if err == nil {
			n, _ := res.RowsAffected()
			counts[table] = n
		}
	}
	if uuid != "" {
		if !uuidRe.MatchString(uuid) {
			writeError(w, http.StatusBadRequest, "UUID格式无效")
			return
		}
		del("user_traffic", "uuid = ?", uuid)
		del("user_traffic_hourly", "uuid = ?", uuid)
	} else {
		del("user_traffic", "1 = 1")
		del("user_traffic_hourly", "1 = 1")
		del("node_connections", "1 = 1")
	}
	h.recordNodeLog(ctx, 0, 0, "traffic-clear", "success", 0,
		"清理流量记录（"+firstNonEmpty(uuid, "全部")+"）：用户流量 "+itoa64(counts["user_traffic"])+
			" 条、小时聚合 "+itoa64(counts["user_traffic_hourly"])+" 条、连接 "+itoa64(counts["node_connections"])+" 条", "", "admin")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "uuid": uuid, "cleared": counts})
}
