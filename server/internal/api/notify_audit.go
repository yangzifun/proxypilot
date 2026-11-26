package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

var (
	// notifyStatusMu 把「冷却检查 + 预占 last_at + 发信」串行化，修复
	// 04:20 通知风暴（多个 Agent 恢复 goroutine 同时通过冷却检查）。
	notifyStatusMu           sync.Mutex
	notifyStatusLastReserved int64
	// 可注入，便于测试不依赖真实 SMTP/直投。
	notifyStatusSendMail = func(h *handlers, to, subject, body string) error {
		return h.sendMail(to, subject, body)
	}
)

// notifyKindFromTitle 把通知标题归一化为稳定 kind。
func notifyKindFromTitle(title string) string {
	switch {
	case strings.Contains(title, "离线"):
		return "offline"
	case strings.Contains(title, "恢复"):
		return "recovery"
	case strings.Contains(title, "流量"):
		return "traffic"
	case strings.Contains(title, "证书"):
		return "certificate"
	default:
		return "status"
	}
}

// writeNotificationLog 每次实际发送批次写一条；server_ids 去重后 JSON。
func (h *handlers) writeNotificationLog(ctx context.Context, kind, title string, serverIDs []int64, recipients, status, errText string) {
	if h.db == nil {
		return
	}
	ids := make([]int64, 0, len(serverIDs))
	seen := map[int64]bool{}
	for _, id := range serverIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rawIDs, _ := json.Marshal(ids)
	if kind == "" {
		kind = notifyKindFromTitle(title)
	}
	_, _ = h.db.ExecContext(ctx,
		`INSERT INTO notification_logs (kind, title, server_ids, recipients, status, error, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		truncateRunes(kind, 32), truncateRunes(title, 191), string(rawIDs),
		truncateRunes(recipients, 2000), status, truncateRunes(errText, 2000), nowMillis())
}

// recordServerStatusEvent 写入上下线事件。
func (h *handlers) recordServerStatusEvent(ctx context.Context, serverID int64, event, reason string, occurredAt, durationSec int64) {
	if h.db == nil || serverID <= 0 {
		return
	}
	if occurredAt <= 0 {
		occurredAt = nowMillis()
	}
	_, _ = h.db.ExecContext(ctx,
		`INSERT INTO server_status_events (server_id, event, reason, occurred_at, duration_sec, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		serverID, event, truncateRunes(reason, 1000), occurredAt, durationSec, nowMillis())
}

// latestOfflineAt 返回该服务器最近一次 offline 事件时间。
func (h *handlers) latestOfflineAt(ctx context.Context, serverID int64) int64 {
	if h.db == nil || serverID <= 0 {
		return 0
	}
	var at int64
	_ = h.db.QueryRowContext(ctx,
		`SELECT COALESCE(
		   NULLIF((SELECT occurred_at FROM server_status_events
		            WHERE server_id = ? AND event = 'offline' ORDER BY id DESC LIMIT 1), 0),
		   (SELECT updated_at FROM servers WHERE id = ?), 0)`, serverID, serverID).Scan(&at)
	return at
}

// GET /api/notification-logs?page=&size=&kind=&status=
func (h *handlers) notificationLogsList(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	qs := r.URL.Query()
	page := atoiDefault(qs.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	size := atoiDefault(qs.Get("size"), 20)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	conds := []string{}
	args := []any{}
	if kind := strings.TrimSpace(qs.Get("kind")); kind != "" {
		conds = append(conds, "kind = ?")
		args = append(args, kind)
	}
	if status := strings.TrimSpace(qs.Get("status")); status != "" {
		conds = append(conds, "status = ?")
		args = append(args, status)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int64
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM notification_logs`+where, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "查询通知记录失败")
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, COALESCE(kind,''), COALESCE(title,''), COALESCE(server_ids,''),
		        COALESCE(recipients,''), COALESCE(status,''), COALESCE(error,''), COALESCE(created_at,0)
		   FROM notification_logs`+where+`
		  ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询通知记录失败")
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var (
			id                          int64
			kind, title, idsRaw, recips string
			status, errText             string
			createdAt                   int64
		)
		if err := rows.Scan(&id, &kind, &title, &idsRaw, &recips, &status, &errText, &createdAt); err != nil {
			continue
		}
		var ids []int64
		_ = json.Unmarshal([]byte(idsRaw), &ids)
		if ids == nil {
			ids = []int64{}
		}
		data = append(data, map[string]any{
			"id": id, "kind": kind, "title": title, "server_ids": ids,
			"recipients": recips, "status": status, "error": errText, "created_at": createdAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "total": total, "page": page, "size": size, "data": data,
	})
}

// GET /api/server-status-events?page=&size=&server_id=&event=&from=&to=
func (h *handlers) serverStatusEventsList(w http.ResponseWriter, r *http.Request) {
	if h.db == nil {
		writeError(w, http.StatusInternalServerError, "数据库未配置")
		return
	}
	qs := r.URL.Query()
	page := atoiDefault(qs.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	size := atoiDefault(qs.Get("size"), 20)
	if size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	conds := []string{}
	args := []any{}
	if id := atoiDefault(qs.Get("server_id"), 0); id > 0 {
		conds = append(conds, "server_id = ?")
		args = append(args, id)
	}
	if event := strings.TrimSpace(qs.Get("event")); event != "" {
		conds = append(conds, "event = ?")
		args = append(args, event)
	}
	if raw := strings.TrimSpace(qs.Get("from")); raw != "" {
		n, ok := parseClientErrorMillis(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "from 必须是毫秒时间戳")
			return
		}
		conds = append(conds, "occurred_at >= ?")
		args = append(args, n)
	}
	if raw := strings.TrimSpace(qs.Get("to")); raw != "" {
		n, ok := parseClientErrorMillis(raw)
		if !ok {
			writeError(w, http.StatusBadRequest, "to 必须是毫秒时间戳")
			return
		}
		conds = append(conds, "occurred_at <= ?")
		args = append(args, n)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int64
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM server_status_events`+where, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "查询上下线记录失败")
		return
	}
	queryArgs := append(append([]any{}, args...), size, (page-1)*size)
	rows, err := h.db.QueryContext(r.Context(),
		`SELECT id, server_id, COALESCE(event,''), COALESCE(reason,''),
		        COALESCE(occurred_at,0), COALESCE(duration_sec,0), COALESCE(created_at,0)
		   FROM server_status_events`+where+`
		  ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询上下线记录失败")
		return
	}
	defer rows.Close()
	data := []map[string]any{}
	for rows.Next() {
		var (
			id, serverID, occurredAt, duration, createdAt int64
			event, reason                                 string
		)
		if err := rows.Scan(&id, &serverID, &event, &reason, &occurredAt, &duration, &createdAt); err != nil {
			continue
		}
		data = append(data, map[string]any{
			"id": id, "server_id": serverID, "event": event, "reason": reason,
			"occurred_at": occurredAt, "duration_sec": duration, "created_at": createdAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "total": total, "page": page, "size": size, "data": data,
	})
}

// markServerOffline 仅在状态真正从非 offline 切换为 offline 时写事件。
func (h *handlers) markServerOffline(ctx context.Context, serverID, now int64, reason string) bool {
	if h.db == nil || serverID <= 0 {
		return false
	}
	res, err := h.db.ExecContext(ctx,
		`UPDATE servers SET last_status = 'offline', last_error = ?, updated_at = ?
		  WHERE id = ? AND COALESCE(last_status,'') <> 'offline'`, reason, now, serverID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	if n <= 0 {
		return false
	}
	h.recordServerStatusEvent(ctx, serverID, "offline", reason, now, 0)
	return true
}

// markServerOnlineIfOffline 原子地抢占 offline→online 切换；并发 Agent 心跳只有一个成功。
func (h *handlers) markServerOnlineIfOffline(ctx context.Context, serverID int64) bool {
	if h.db == nil || serverID <= 0 {
		return false
	}
	res, err := h.db.ExecContext(ctx,
		`UPDATE servers SET last_status = 'online' WHERE id = ? AND last_status = 'offline'`, serverID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}
