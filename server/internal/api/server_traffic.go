package api

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 服务器流量配额：按上行/下行/合计统计，支持按月指定日期重置。

type serverTrafficLimit struct {
	ServerID    int64  `json:"server_id"`
	Enabled     int64  `json:"enabled"`
	Mode        string `json:"mode"`
	LimitBytes  int64  `json:"limit_bytes"`
	ResetDay    int    `json:"reset_day"`
	ResetHour   int    `json:"reset_hour"`
	Action      string `json:"action"`
	WarnPercent int    `json:"warn_percent"`
}

func clampTrafficLimit(l *serverTrafficLimit) {
	if l.Mode != "rx" && l.Mode != "tx" && l.Mode != "both" {
		l.Mode = "both"
	}
	if l.Action != "notify" && l.Action != "suspend" {
		l.Action = "notify"
	}
	if l.ResetDay < 1 || l.ResetDay > 31 {
		l.ResetDay = 1
	}
	if l.ResetHour < 0 || l.ResetHour > 23 {
		l.ResetHour = 0
	}
	if l.WarnPercent < 1 || l.WarnPercent > 100 {
		l.WarnPercent = 80
	}
	if l.LimitBytes < 0 {
		l.LimitBytes = 0
	}
}

// trafficPeriodStart 返回 now 所在计费周期的起点（按 reset_day/reset_hour，本地时区）。
func trafficPeriodStart(now time.Time, day, hour int) time.Time {
	loc := now.Location()
	y, m := now.Year(), now.Month()
	candidate := time.Date(y, m, 1, hour, 0, 0, 0, loc)
	lastDay := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	candidate = time.Date(y, m, day, hour, 0, 0, 0, loc)
	if now.Before(candidate) {
		pm := time.Date(y, m-1, 1, hour, 0, 0, 0, loc)
		plast := time.Date(pm.Year(), pm.Month()+1, 0, 0, 0, 0, 0, loc).Day()
		pd := day
		if pd > plast {
			pd = plast
		}
		candidate = time.Date(pm.Year(), pm.Month(), pd, hour, 0, 0, 0, loc)
	}
	return candidate
}

// trafficNextReset 返回周期起点之后的下一次重置时间。
func trafficNextReset(periodStart time.Time, day, hour int) time.Time {
	loc := periodStart.Location()
	y, m := periodStart.Year(), periodStart.Month()+1
	lastDay := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(y, m, day, hour, 0, 0, 0, loc)
}

func (h *handlers) loadTrafficLimit(ctx context.Context, serverID int64) serverTrafficLimit {
	l := serverTrafficLimit{ServerID: serverID, Mode: "both", Action: "notify", ResetDay: 1, WarnPercent: 80}
	_ = h.db.QueryRowContext(ctx,
		"SELECT enabled, mode, limit_bytes, reset_day, reset_hour, action, warn_percent FROM server_traffic_limits WHERE server_id = ?",
		serverID).Scan(&l.Enabled, &l.Mode, &l.LimitBytes, &l.ResetDay, &l.ResetHour, &l.Action, &l.WarnPercent)
	clampTrafficLimit(&l)
	return l
}

// accrueServerTraffic 用累计计数器差分累计本周期用量。
func (h *handlers) accrueServerTraffic(ctx context.Context, serverID, rxTotal, txTotal int64) {
	if serverID <= 0 {
		return
	}
	lim := h.loadTrafficLimit(ctx, serverID)
	now := time.Now()
	period := trafficPeriodStart(now, lim.ResetDay, lim.ResetHour).UnixMilli()

	var rx, tx, lastRx, lastTx, lastAt int64
	err := h.db.QueryRowContext(ctx,
		"SELECT rx_bytes, tx_bytes, last_rx_total, last_tx_total, last_sample_at FROM server_traffic_usage WHERE server_id = ? AND period_start = ?",
		serverID, period).Scan(&rx, &tx, &lastRx, &lastTx, &lastAt)
	if err == sql.ErrNoRows {
		// 首条采样：以当前计数器为起点，本周期从 0 开始
		_, _ = h.db.ExecContext(ctx,
			"INSERT INTO server_traffic_usage (server_id, period_start, rx_bytes, tx_bytes, last_rx_total, last_tx_total, last_sample_at) VALUES (?,?,0,0,?,?,?) "+
				"ON DUPLICATE KEY UPDATE last_rx_total = VALUES(last_rx_total), last_tx_total = VALUES(last_tx_total), last_sample_at = VALUES(last_sample_at)",
			serverID, period, rxTotal, txTotal, now.UnixMilli())
		return
	}
	if err != nil {
		return
	}
	dRx := rxTotal - lastRx
	if dRx < 0 { // 计数器重置（重启/网卡重连）
		dRx = rxTotal
	}
	dTx := txTotal - lastTx
	if dTx < 0 {
		dTx = txTotal
	}
	rx += dRx
	tx += dTx
	_, _ = h.db.ExecContext(ctx,
		"UPDATE server_traffic_usage SET rx_bytes = ?, tx_bytes = ?, last_rx_total = ?, last_tx_total = ?, last_sample_at = ? WHERE server_id = ? AND period_start = ?",
		rx, tx, rxTotal, txTotal, now.UnixMilli(), serverID, period)
	h.evalTrafficState(ctx, serverID, lim, rx, tx, period)
}

func trafficUsed(lim serverTrafficLimit, rx, tx int64) int64 {
	switch lim.Mode {
	case "rx":
		return rx
	case "tx":
		return tx
	default:
		return rx + tx
	}
}

// evalTrafficState 计算 normal/warning/exceeded 并处理超限动作。
func (h *handlers) evalTrafficState(ctx context.Context, serverID int64, lim serverTrafficLimit, rx, tx, period int64) {
	used := trafficUsed(lim, rx, tx)
	state := "normal"
	if lim.Enabled == 1 && lim.LimitBytes > 0 {
		pct := used * 100 / lim.LimitBytes
		if used >= lim.LimitBytes {
			state = "exceeded"
		} else if pct >= int64(lim.WarnPercent) {
			state = "warning"
		}
	}
	var prev string
	_ = h.db.QueryRowContext(ctx, "SELECT COALESCE(traffic_state,'normal') FROM servers WHERE id = ?", serverID).Scan(&prev)
	if state == prev {
		return
	}
	_, _ = h.db.ExecContext(ctx, "UPDATE servers SET traffic_state = ? WHERE id = ?", state, serverID)
	if state == "exceeded" {
		_, _ = h.db.ExecContext(ctx, "UPDATE server_traffic_usage SET exceeded_at = ? WHERE server_id = ? AND period_start = ?",
			nowMillis(), serverID, period)
		h.log.Warn("服务器流量超出上限", "server_id", serverID, "used", used, "limit", lim.LimitBytes, "action", lim.Action)
		h.recordNodeLog(ctx, serverID, 0, "traffic-limit", "failed", 0,
			fmt.Sprintf("流量超限：已用 %d MB / 上限 %d MB（%s）", used>>20, lim.LimitBytes>>20, lim.Action), "", "cron")
	}
}

// ---------------- API ----------------

func (h *handlers) serverTrafficLimitsList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT s.id AS server_id, COALESCE(s.name,'') AS name, COALESCE(s.traffic_state,'normal') AS state,
		        COALESCE(l.enabled,0) AS enabled, COALESCE(l.mode,'both') AS mode, COALESCE(l.limit_bytes,0) AS limit_bytes,
		        COALESCE(l.reset_day,1) AS reset_day, COALESCE(l.reset_hour,0) AS reset_hour,
		        COALESCE(l.action,'notify') AS action, COALESCE(l.warn_percent,80) AS warn_percent,
		        COALESCE(u.rx_bytes,0) AS rx_bytes, COALESCE(u.tx_bytes,0) AS tx_bytes, COALESCE(u.period_start,0) AS period_start
		 FROM servers s
		 LEFT JOIN server_traffic_limits l ON l.server_id = s.id
		 LEFT JOIN server_traffic_usage u ON u.server_id = s.id
		   AND u.period_start = (SELECT MAX(period_start) FROM server_traffic_usage x WHERE x.server_id = s.id)
		 ORDER BY s.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}

func (h *handlers) serverTrafficLimitGet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	lim := h.loadTrafficLimit(r.Context(), id)
	var rx, tx, period, exceeded int64
	_ = h.db.QueryRowContext(r.Context(),
		"SELECT rx_bytes, tx_bytes, period_start, exceeded_at FROM server_traffic_usage WHERE server_id = ? ORDER BY period_start DESC LIMIT 1",
		id).Scan(&rx, &tx, &period, &exceeded)
	var state string
	_ = h.db.QueryRowContext(r.Context(), "SELECT COALESCE(traffic_state,'normal') FROM servers WHERE id = ?", id).Scan(&state)
	next := int64(0)
	if period > 0 {
		next = trafficNextReset(time.UnixMilli(period), lim.ResetDay, lim.ResetHour).UnixMilli()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "limit": lim, "state": state,
		"rx_bytes": rx, "tx_bytes": tx, "period_start": period, "next_reset": next,
		"used_bytes": trafficUsed(lim, rx, tx),
	})
}

func (h *handlers) serverTrafficLimitSet(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var body serverTrafficLimit
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	body.ServerID = id
	clampTrafficLimit(&body)
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO server_traffic_limits (server_id, enabled, mode, limit_bytes, reset_day, reset_hour, action, warn_percent, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)
		 ON DUPLICATE KEY UPDATE enabled=VALUES(enabled), mode=VALUES(mode), limit_bytes=VALUES(limit_bytes),
		   reset_day=VALUES(reset_day), reset_hour=VALUES(reset_hour), action=VALUES(action), warn_percent=VALUES(warn_percent), updated_at=VALUES(updated_at)`,
		id, body.Enabled, body.Mode, body.LimitBytes, body.ResetDay, body.ResetHour, body.Action, body.WarnPercent, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	// 重新评估一次，便于上限调大后立即解除超限
	h.recalcServerTrafficState(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "limit": body})
}

func (h *handlers) recalcServerTrafficState(ctx context.Context, serverID int64) {
	lim := h.loadTrafficLimit(ctx, serverID)
	var rx, tx, period int64
	_ = h.db.QueryRowContext(ctx,
		"SELECT rx_bytes, tx_bytes, period_start FROM server_traffic_usage WHERE server_id = ? ORDER BY period_start DESC LIMIT 1",
		serverID).Scan(&rx, &tx, &period)
	h.evalTrafficState(ctx, serverID, lim, rx, tx, period)
}

// serverTrafficLimitReset 清零当前周期用量（不动累计计数器）。
func (h *handlers) serverTrafficLimitReset(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	lim := h.loadTrafficLimit(r.Context(), id)
	var lastRx, lastTx int64
	_ = h.db.QueryRowContext(r.Context(),
		"SELECT last_rx_total, last_tx_total FROM server_traffic_usage WHERE server_id = ? ORDER BY period_start DESC LIMIT 1",
		id).Scan(&lastRx, &lastTx)
	now := time.Now()
	period := trafficPeriodStart(now, lim.ResetDay, lim.ResetHour).UnixMilli()
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO server_traffic_usage (server_id, period_start, rx_bytes, tx_bytes, last_rx_total, last_tx_total, last_sample_at, exceeded_at)
		 VALUES (?,?,0,0,?,?,?,0)
		 ON DUPLICATE KEY UPDATE rx_bytes=0, tx_bytes=0, exceeded_at=0`,
		id, period, lastRx, lastTx, now.UnixMilli())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "重置失败")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), "UPDATE servers SET traffic_state='normal' WHERE id = ?", id)
	h.log.Info("流量周期已手动重置", "server_id", id, "period_start", period)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "period_start": period})
}

// serverTrafficUnsuspend 手动解除超限暂停（不改用量）。
func (h *handlers) serverTrafficUnsuspend(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_, _ = h.db.ExecContext(r.Context(), "UPDATE servers SET traffic_state='normal' WHERE id = ?", id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// cronTrafficReset 每分钟检查是否到达重置时间。
func (h *handlers) cronTrafficReset(ctx context.Context, now int64) {
	rows, err := h.db.QueryContext(ctx, "SELECT server_id, reset_day, reset_hour FROM server_traffic_limits")
	if err != nil {
		return
	}
	type item struct {
		id      int64
		day, hr int
	}
	var items []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.id, &it.day, &it.hr) == nil {
			items = append(items, it)
		}
	}
	rows.Close()
	if len(items) == 0 {
		return
	}
	nowT := time.UnixMilli(now)
	for _, it := range items {
		var period int64
		err := h.db.QueryRowContext(ctx,
			"SELECT period_start FROM server_traffic_usage WHERE server_id = ? ORDER BY period_start DESC LIMIT 1", it.id).Scan(&period)
		if err != nil || period == 0 {
			continue
		}
		next := trafficNextReset(time.UnixMilli(period), it.day, it.hr)
		if nowT.Before(next) {
			continue
		}
		var lastRx, lastTx int64
		_ = h.db.QueryRowContext(ctx,
			"SELECT last_rx_total, last_tx_total FROM server_traffic_usage WHERE server_id = ? ORDER BY period_start DESC LIMIT 1",
			it.id).Scan(&lastRx, &lastTx)
		_, _ = h.db.ExecContext(ctx,
			"INSERT INTO server_traffic_usage (server_id, period_start, rx_bytes, tx_bytes, last_rx_total, last_tx_total, last_sample_at) VALUES (?,?,0,0,?,?,?)",
			it.id, next.UnixMilli(), lastRx, lastTx, now)
		_, _ = h.db.ExecContext(ctx, "UPDATE servers SET traffic_state='normal' WHERE id = ?", it.id)
		h.log.Info("流量周期已自动重置", "server_id", it.id, "period_start", next.Format(time.RFC3339))
	}
}

// trafficSuspendedServerIDs 返回因超限需要从订阅中排除的服务器。
func (h *handlers) trafficSuspendedServerIDs(ctx context.Context) map[int64]bool {
	out := map[int64]bool{}
	rows, err := h.db.QueryContext(ctx,
		`SELECT s.id FROM servers s JOIN server_traffic_limits l ON l.server_id = s.id
		 WHERE l.enabled = 1 AND l.action = 'suspend' AND s.traffic_state = 'exceeded'`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out[id] = true
		}
	}
	return out
}

// filterSuspendedLines 过滤掉超限服务器的订阅行（configs.singbox_node_id → server_id）。
func (h *handlers) filterSuspendedLines(ctx context.Context, lines []string, uuid string) []string {
	suspended := h.trafficSuspendedServerIDs(ctx)
	if len(suspended) == 0 {
		return lines
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT n.server_id, c.config_data FROM configs c JOIN singbox_nodes n ON n.id = c.singbox_node_id WHERE c.uuid = ?`, uuid)
	if err != nil {
		return lines
	}
	defer rows.Close()
	drop := map[string]bool{}
	for rows.Next() {
		var sid int64
		var data sql.NullString
		if rows.Scan(&sid, &data) == nil && data.Valid && suspended[sid] {
			drop[data.String] = true
		}
	}
	if len(drop) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if !drop[l] {
			out = append(out, l)
		}
	}
	return out
}

// 供其他文件使用的简单提示（避免未使用告警）。
var _ = strings.TrimSpace
