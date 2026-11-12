package api

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
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

// statsActiveWindowMs 「活跃」判定窗口：2 分钟（采集间隔约 30s，容忍一次抖动）
var inboundUUIDRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

const statsActiveWindowMs int64 = 120_000

// =====================================================================
//  节点级「在线用户」与「实时速率」口径（V1.3 修复节点连接数 0 / 实时 0）
//
//  为什么不能拿瞬时连接数当主指标：
//   1. sing-box 的 clash_api /connections 只反映「采样那一刻」还活着的连接。
//      客户端 url-test 健康检查是亚秒级连接（实测：同源 IP 依次走
//      hysteria2/anytls/vless-ws/ss/vless-reality 各打一发 www.google.com 就断），
//      60s/次级别的采样几乎必然落在两次连接之间 → 面板恒显示 0，但用户明明在用。
//   2. 产品最初的需求是「用户在线情况」，不是「排障用的瞬时连接数」。
//
//  因此：节点主指标「在线用户」= 近 5 分钟在该节点有流量增量的去重用户数
//  （user_node_recent.last_ts，只在 dUp/dDown>0 时刷新，见 ingestUserCounters）。
//  这个口径与 mihomo 节点 online_count、UserTraffic「按节点 · 活跃 UUID」一致，
//  只用真实上报数据推导，不做任何缓存填充；瞬时连接数仍原样返回（conn_count），
//  供 tooltip / 排障使用。
//
//  「节点实时」同理：单次采样窗口（当前约 10~60s）经常正好落在两次突发之间，
//  直接展示单次采样的差分会抖成 0。这里在进程内保留最近 5 分钟的真实上报样本，
//  用「窗口首尾原始计数差 / 实际跨度」算平均速率；样本不足或 sing-box 重启
//  （计数回退）时如实回退到单次采样速率，窗口内真的没有流量时仍然显示 0。
// =====================================================================

const (
	// statsNodeOnlineWindowMs 节点「在线用户」窗口：5 分钟。
	// 与 mihomo 节点 online_count（mihomo.go）、traffic_nodes.go 的 5 分钟窗口一致。
	statsNodeOnlineWindowMs int64 = 300_000
	// statsNodeRateWindowMs 「节点实时」平滑窗口：5 分钟。
	statsNodeRateWindowMs int64 = 300_000
	// statsNodeRateMaxSamples 单节点最多保留的采样数（防止内存无上限增长）。
	statsNodeRateMaxSamples = 240
	// statsNodeRateMinSpanMs 计算窗口平均速率所需的最小跨度（不足则回退单次采样）。
	statsNodeRateMinSpanMs int64 = 10_000
)

// nodeRateSample 一次真实统计上报里的节点原始计数（来自 clash_api 的累计值）。
type nodeRateSample struct {
	at        int64 // 采样时间（服务端入库时间，毫秒）
	up, down  int64 // sing-box 进程内累计上下行（原始计数）
	connCount int64 // 采样时刻的连接数（排障用）
}

// nodeRateHistory 进程内保存每个节点最近的采样，用于计算窗口平均速率与连接峰值。
// 只保存 Agent 真实上报的数值，不做任何插值/填充；进程重启后窗口重新累积，
// 期间接口回退到单次采样速率（不伪造数据）。
type nodeRateHistory struct {
	mu   sync.Mutex
	data map[int64][]nodeRateSample
}

// statsNodeRates 全局窗口（面板为单进程，无需持久化）。
var statsNodeRates = newNodeRateHistory()

func newNodeRateHistory() *nodeRateHistory {
	return &nodeRateHistory{data: map[int64][]nodeRateSample{}}
}

// record 追加一条真实采样并裁掉窗口外的旧样本。
func (h *nodeRateHistory) record(nodeID, at, up, down, connCount int64) {
	if nodeID <= 0 || at <= 0 {
		return
	}
	// 负值防御：Agent 上报异常（负数）按 0 处理，避免污染窗口平均值。
	if up < 0 {
		up = 0
	}
	if down < 0 {
		down = 0
	}
	if connCount < 0 {
		connCount = 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list := append(h.data[nodeID], nodeRateSample{at: at, up: up, down: down, connCount: connCount})
	cutoff := at - statsNodeRateWindowMs
	keep := make([]nodeRateSample, 0, len(list))
	for _, s := range list {
		if s.at >= cutoff {
			keep = append(keep, s)
		}
	}
	if len(keep) > statsNodeRateMaxSamples {
		keep = keep[len(keep)-statsNodeRateMaxSamples:]
	}
	h.data[nodeID] = keep
}

// window 返回窗口内（at >= now-statsNodeRateWindowMs）的样本副本，按时间升序。
func (h *nodeRateHistory) window(nodeID, now int64) []nodeRateSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.data[nodeID]
	if len(list) == 0 {
		return nil
	}
	cutoff := now - statsNodeRateWindowMs
	out := make([]nodeRateSample, 0, len(list))
	for _, s := range list {
		if s.at >= cutoff {
			out = append(out, s)
		}
	}
	return out
}

// reset 清空窗口（仅单测使用，避免用例之间互相污染）。
func (h *nodeRateHistory) reset() {
	h.mu.Lock()
	h.data = map[int64][]nodeRateSample{}
	h.mu.Unlock()
}

// nodeWindowSummary 汇总一个节点的窗口指标：
//
//	upAvg/downAvg = (窗口最新原始计数 - 窗口最早原始计数) / 实际跨度（字节/秒）
//	connPeak      = 窗口内单次采样的连接数峰值
//	spanMs        = 实际参与计算的跨度（0 表示回退到单次采样速率）
//
// 回退规则（宁可显示单次采样，也不编造趋势）：
//   - 窗口内不足 2 个样本，或跨度 < statsNodeRateMinSpanMs；
//   - 原始计数回退（sing-box 重启，last < first），窗口不可比。
func nodeWindowSummary(samples []nodeRateSample, instUpRate, instDownRate int64) (upAvg, downAvg, spanMs, connPeak int64) {
	for _, s := range samples {
		if s.connCount > connPeak {
			connPeak = s.connCount
		}
	}
	if len(samples) < 2 {
		return instUpRate, instDownRate, 0, connPeak
	}
	first, last := samples[0], samples[len(samples)-1]
	// 负值/时间乱序防御：任一异常都回退单次采样，不参与窗口平均。
	if first.up < 0 || first.down < 0 || last.up < 0 || last.down < 0 {
		return instUpRate, instDownRate, 0, connPeak
	}
	spanMs = last.at - first.at
	if spanMs < statsNodeRateMinSpanMs {
		return instUpRate, instDownRate, 0, connPeak
	}
	if last.up < first.up || last.down < first.down {
		return instUpRate, instDownRate, 0, connPeak
	}
	return (last.up - first.up) * 1000 / spanMs,
		(last.down - first.down) * 1000 / spanMs,
		spanMs, connPeak
}

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
	if s := h.openAgentToken(enc); s != "" {
		return s
	}
	secret := randomStatsSecret()
	if secret == "" {
		return ""
	}
	encNew, err := h.sealAgentToken(secret)
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
		if dUp < 0 {
			dUp = 0
		}
		if dDown < 0 {
			dDown = 0
		}
		// 关键：本次采样没有任何流量增量 → 用户在该节点上处于空闲，不刷新活跃时间。
		// （历史实现每次采样都刷新，导致长期不用的用户也一直显示「活跃中」）
		if dUp == 0 && dDown == 0 {
			continue
		}
		_, _ = h.db.ExecContext(ctx, `INSERT INTO user_node_recent (uuid, node_id, last_ts, up, down)
		      VALUES (?, ?, ?, ?, ?)
		      ON DUPLICATE KEY UPDATE last_ts = VALUES(last_ts), up = up + VALUES(up), down = down + VALUES(down)`,
			uuid, nodeID, now, dUp, dDown)
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
	realTimeByIP := map[string]bool{}
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
		// 入站 tag 含 UUID（按用户拆分的入站）时同样视为在线
		if tag := jsonStr(meta["inbound"]); tag != "" {
			if u := inboundUUIDRe.FindString(tag); u != "" {
				onlineUUIDs[u] = true
			}
		}
		srcIP := jsonStr(meta["sourceIP"])
		if srcIP != "" {
			realTimeByIP[srcIP] = true
		}
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
	if len(realTimeByIP) > 0 && len(realTimeByIP) <= 200 {
		ips := make([]any, 0, len(realTimeByIP))
		for ip := range realTimeByIP {
			ips = append(ips, ip)
		}
		ph := make([]string, len(ips))
		for i := range ips {
			ph[i] = "?"
		}
		q := "SELECT DISTINCT uuid FROM config_access_logs WHERE client_ip IN (" + strings.Join(ph, ",") + ") AND uuid <> ''"
		if r2, err2 := h.db.QueryContext(ctx, q, ips...); err2 == nil {
			for r2.Next() {
				var u string
				if r2.Scan(&u) == nil && regexpUUID.MatchString(u) {
					onlineUUIDs[u] = true
				}
			}
			r2.Close()
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

	// task-17：先更新「单调累计」（历史求和），再写原始快照。
	// 首次见到该节点时，lifetime 初值取旧快照累计（迁移 0054 也会回填），
	// last_raw 取本轮原始计数，避免上线后累计归零。
	baseUp, baseDown := int64(0), int64(0)
	if hasPrev {
		baseUp, baseDown = prevUp, prevDown
	}
	if err := h.accumulateNodeLifetime(ctx, nodeID, upTotal, downTotal, baseUp, baseDown, now); err != nil {
		h.log.Warn("写入节点累计流量失败", "err", err, "node_id", nodeID)
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
		return
	}
	// 记录到进程内窗口：「节点实时」用 5 分钟平均速率展示，避免单次采样落在两次突发
	// 之间时抖动成 0；只存 Agent 真实上报的原始计数，不做填充。
	statsNodeRates.record(nodeID, now, upTotal, downTotal, connCount)
}

// lifetimeDelta 计算「单调累计」应增加的量：
//   - 正常递增（raw >= last）：delta = raw - last；
//   - 计数回退（sing-box 重启，raw < last）：delta = raw（重启后从 0 重新开始的新流量）；
//   - 负数/异常值按 0 处理。
func lifetimeDelta(raw, last int64) int64 {
	if raw < 0 {
		raw = 0
	}
	if last < 0 {
		last = 0
	}
	if raw >= last {
		return raw - last
	}
	return raw
}

// accumulateNodeLifetime 原子地更新节点历史累计（带一次并发插入重试）。
func (h *handlers) accumulateNodeLifetime(ctx context.Context, nodeID, rawUp, rawDown, baseUp, baseDown, now int64) error {
	err := h.accumulateNodeLifetimeOnce(ctx, nodeID, rawUp, rawDown, baseUp, baseDown, now)
	if err != nil && isDuplicateKeyError(err) {
		// 并发首轮上报：另一请求刚插入同一 node_id 的行 → 重新走「增量更新」路径。
		return h.accumulateNodeLifetimeOnce(ctx, nodeID, rawUp, rawDown, baseUp, baseDown, now)
	}
	return err
}

// isDuplicateKeyError 识别 MySQL 1062（fake/测试驱动也可能用英文文本）。
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") || strings.Contains(msg, "duplicate key")
}

// accumulateNodeLifetimeOnce 单事务完成一次累计更新。
//
// 表 node_traffic_lifetime（迁移 0054）：
//
//	up_total/down_total = 历史求和（单调递增，重启不回退，API 返回这个值）
//	last_raw_up/down    = 上一轮原始计数
//
// 并发：单事务 + SELECT ... FOR UPDATE，同一节点的并发/重复上报串行化，
// 重复上报同一 raw 时 delta=0，不会重复计数。
func (h *handlers) accumulateNodeLifetimeOnce(ctx context.Context, nodeID, rawUp, rawDown, baseUp, baseDown, now int64) error {
	if nodeID <= 0 {
		return nil
	}
	if rawUp < 0 {
		rawUp = 0
	}
	if rawDown < 0 {
		rawDown = 0
	}
	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var upTotal, downTotal, lastUp, lastDown int64
	err = tx.QueryRowContext(ctx,
		`SELECT up_total, down_total, last_raw_up, last_raw_down
		   FROM node_traffic_lifetime WHERE node_id = ? FOR UPDATE`, nodeID).
		Scan(&upTotal, &downTotal, &lastUp, &lastDown)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 首轮：lifetime 初值取旧快照累计（迁移 0054 已回填存量节点；
		// 新节点没有快照时 base=0），last_raw 取本轮原始计数。
		if baseUp > 0 || baseDown > 0 {
			upTotal, downTotal = baseUp, baseDown
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO node_traffic_lifetime
			   (node_id, up_total, down_total, last_raw_up, last_raw_down, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			nodeID, upTotal, downTotal, rawUp, rawDown, now); err != nil {
			return err
		}
	case err != nil:
		return err
	default:
		dUp := lifetimeDelta(rawUp, lastUp)
		dDown := lifetimeDelta(rawDown, lastDown)
		if _, err := tx.ExecContext(ctx,
			`UPDATE node_traffic_lifetime
			    SET up_total = ?, down_total = ?, last_raw_up = ?, last_raw_down = ?, updated_at = ?
			  WHERE node_id = ?`,
			upTotal+dUp, downTotal+dDown, rawUp, rawDown, now, nodeID); err != nil {
			return err
		}
	}
	return tx.Commit()
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

	recents := map[string]int64{}
	if rrows, err := h.db.QueryContext(ctx, "SELECT uuid, MAX(last_ts) FROM user_node_recent GROUP BY uuid"); err == nil {
		for rrows.Next() {
			var u string
			var ts int64
			if rrows.Scan(&u, &ts) == nil {
				recents[u] = ts
			}
		}
		rrows.Close()
	}

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
		if r := recents[uuid]; r > lastAt {
			lastAt = r
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

	recentNodes := []map[string]any{}
	if rrows, err := h.db.QueryContext(ctx,
		`SELECT r.node_id, COALESCE(n.name,''), r.up, r.down, r.last_ts
		  FROM user_node_recent r LEFT JOIN singbox_nodes n ON n.id = r.node_id
		  WHERE r.uuid = ? AND r.last_ts >= ? ORDER BY r.last_ts DESC`,
		uuid, nowMillis()-180000); err == nil {
		for rrows.Next() {
			var nodeID, up, down, lastTS int64
			var nodeName string
			if rrows.Scan(&nodeID, &nodeName, &up, &down, &lastTS) == nil {
				recentNodes = append(recentNodes, map[string]any{
					"node_id": nodeID, "node_name": nodeName,
					"up": up, "down": down, "last_ts": lastTS,
				})
			}
		}
		rrows.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "uuid": uuid, "owner": owner, "email": email,
		"up": sumUp, "down": sumDown, "last_active_at": lastActive,
		"active": lastActive > 0 && nowMillis()-lastActive <= statsActiveWindowMs,
		"series": series, "nodes": nodes, "recent_nodes": recentNodes, "start": start, "end": end,
	})
}

// nodeOnlineCounts 统计每个节点「近 statsNodeOnlineWindowMs 分钟有流量增量」的去重用户数。
//
// 数据来源 user_node_recent.last_ts：ingestUserCounters 只在本轮有流量增量
// （dUp/dDown > 0）时刷新它，因此它就是「最近真的在这台节点上用网的用户」，
// 不用采样时刻的连接数冒充在线。表不存在 / 查询失败时降级为全 0（页面仍可用）。
func (h *handlers) nodeOnlineCounts(ctx context.Context, now int64) map[int64]int64 {
	out := map[int64]int64{}
	rows, err := h.db.QueryContext(ctx,
		`SELECT node_id, COUNT(DISTINCT uuid) FROM user_node_recent
		  WHERE last_ts >= ? GROUP BY node_id`, now-statsNodeOnlineWindowMs)
	if err != nil {
		h.log.Warn("查询节点在线用户数失败（降级为 0）", "err", err)
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var nodeID, n int64
		if rows.Scan(&nodeID, &n) == nil && nodeID > 0 {
			out[nodeID] = n
		}
	}
	return out
}

// GET /api/stats/traffic/nodes
//
// task-17：up_total/down_total 返回「单调累计」（node_traffic_lifetime），
// 不再直接返回 sing-box 进程内计数；后者只用于算速率与增量。
// 兼容旧数据：lifetime 行尚未建立时（迁移执行前/从未上报）回退到旧快照值。
//
// V1.3 统计口径修正：新增 online_count（近 5 分钟活跃用户数）与
// up_rate_avg/down_rate_avg（近 5 分钟平均速率）、conn_peak（窗口内连接峰值）；
// conn_count / up_rate / down_rate 仍保留原始单次采样值（tooltip、排障用）。
func (h *handlers) statsTrafficNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT n.id AS node_id, n.name AS node_name,
	      n.server_id, COALESCE(s.name,'') AS server_name, n.stats_enabled,
	      COALESCE(snap.sampled_at,0) AS sampled_at, COALESCE(snap.conn_count,0) AS conn_count,
	      COALESCE(lt.up_total, snap.up_total, 0) AS up_total,
	      COALESCE(lt.down_total, snap.down_total, 0) AS down_total,
	      COALESCE(snap.up_rate,0) AS up_rate, COALESCE(snap.down_rate,0) AS down_rate,
	      COALESCE(snap.err_text,'') AS stats_error
	    FROM singbox_nodes n
	    LEFT JOIN servers s ON s.id = n.server_id
	    LEFT JOIN node_stats_snapshot snap ON snap.node_id = n.id
	    LEFT JOIN node_traffic_lifetime lt ON lt.node_id = n.id
	    ORDER BY n.id ASC`)
	if err != nil && strings.Contains(err.Error(), "node_traffic_lifetime") {
		// 迁移 0054 尚未执行（AutoMigrate=false 的旧库）：退回只读原始快照，
		// 保证统计页面可用；累计语义会在迁移执行后恢复。
		h.log.Warn("node_traffic_lifetime 不存在，已回退原始快照展示；请执行迁移 0054", "err", err)
		rows, err = queryRowsAsMaps(r.Context(), h.db, `SELECT n.id AS node_id, n.name AS node_name,
		      n.server_id, COALESCE(s.name,'') AS server_name, n.stats_enabled,
		      COALESCE(snap.sampled_at,0) AS sampled_at, COALESCE(snap.conn_count,0) AS conn_count,
		      COALESCE(snap.up_total,0) AS up_total, COALESCE(snap.down_total,0) AS down_total,
		      COALESCE(snap.up_rate,0) AS up_rate, COALESCE(snap.down_rate,0) AS down_rate,
		      COALESCE(snap.err_text,'') AS stats_error
		    FROM singbox_nodes n
		    LEFT JOIN servers s ON s.id = n.server_id
		    LEFT JOIN node_stats_snapshot snap ON snap.node_id = n.id
		    ORDER BY n.id ASC`)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点统计失败")
		return
	}
	nowMs := nowMillis()
	onlineByNode := h.nodeOnlineCounts(r.Context(), nowMs)
	data := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		nodeID := mapInt(row, "node_id", 0)
		sampledAt := mapInt(row, "sampled_at", 0)
		// 超过 5 分钟没上报视为失联，避免展示过期的错误信息
		stale := sampledAt == 0 || nowMs-sampledAt > 300_000
		instUp := mapInt(row, "up_rate", 0)
		instDown := mapInt(row, "down_rate", 0)
		upAvg, downAvg, rateSpanMs, connPeak := nodeWindowSummary(
			statsNodeRates.window(nodeID, nowMs), instUp, instDown)
		data = append(data, map[string]any{
			"node_id": mapInt(row, "node_id", 0), "node_name": mapStr(row, "node_name", ""),
			"server_id":     mapInt(row, "server_id", 0),
			"server_name":   mapStr(row, "server_name", ""),
			"stats_enabled": mapInt(row, "stats_enabled", 1) != 0,
			"sampled_at":    mapInt(row, "sampled_at", 0),
			"conn_count":    mapInt(row, "conn_count", 0),
			"up_total":      mapInt(row, "up_total", 0), "down_total": mapInt(row, "down_total", 0),
			"up_rate": instUp, "down_rate": instDown,
			"up_rate_avg": upAvg, "down_rate_avg": downAvg,
			"rate_window_sec":   rateSpanMs / 1000,
			"online_count":      onlineByNode[nodeID],
			"active_window_sec": statsNodeOnlineWindowMs / 1000,
			"conn_peak":         connPeak,
			"error":             mapStr(row, "stats_error", ""), "stale": stale,
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
