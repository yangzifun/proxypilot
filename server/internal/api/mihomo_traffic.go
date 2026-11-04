package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------------- mihomo 按用户流量（/connections 差分） ----------------

type mihomoConnSample struct {
	Up   int64
	Down int64
}

var (
	mihomoConnMu    sync.Mutex
	mihomoConnState = map[int64]map[string]mihomoConnSample{} // nodeID -> connID -> last bytes
)

func mihomoNewReportToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// POST /api/mihomo/report?node=<id>&token=<report_token>
// 接收 mihomo /connections 的原始 JSON，差分后写入按用户流量表。
func (h *handlers) mihomoConnReport(w http.ResponseWriter, r *http.Request) {
	nodeID := intOrJS(r.URL.Query().Get("node"), 0, 0, 1<<40)
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if nodeID <= 0 || token == "" {
		writeError(w, http.StatusBadRequest, "缺少 node/token")
		return
	}
	var want string
	if err := h.db.QueryRowContext(r.Context(),
		`SELECT COALESCE(report_token,'') FROM mihomo_nodes WHERE id = ? LIMIT 1`, nodeID).Scan(&want); err != nil || want == "" || want != token {
		writeError(w, http.StatusForbidden, "token 无效")
		return
	}
	var body struct {
		Connections []struct {
			ID       string `json:"id"`
			Upload   int64  `json:"upload"`
			Download int64  `json:"download"`
			Metadata struct {
				InboundUser string `json:"inboundUser"`
				InboundName string `json:"inboundName"`
			} `json:"metadata"`
		} `json:"connections"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "JSON 无效")
		return
	}
	now := time.Now()
	hourTs := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location()).UnixMilli()
	nowMs := now.UnixMilli()

	type agg struct {
		up, down int64
		last     int64
	}
	perUser := map[string]*agg{}

	mihomoConnMu.Lock()
	state := mihomoConnState[nodeID]
	if state == nil {
		state = map[string]mihomoConnSample{}
		mihomoConnState[nodeID] = state
	}
	seen := map[string]bool{}
	for _, c := range body.Connections {
		user := strings.ToLower(strings.TrimSpace(c.Metadata.InboundUser))
		if user == "" || !uuidRe.MatchString(user) {
			continue
		}
		seen[c.ID] = true
		prev, ok := state[c.ID]
		dUp, dDown := c.Upload, c.Download
		if ok {
			dUp = c.Upload - prev.Up
			dDown = c.Download - prev.Down
		}
		if dUp < 0 {
			dUp = 0
		}
		if dDown < 0 {
			dDown = 0
		}
		state[c.ID] = mihomoConnSample{Up: c.Upload, Down: c.Download}
		if dUp == 0 && dDown == 0 {
			continue
		}
		a := perUser[user]
		if a == nil {
			a = &agg{}
			perUser[user] = a
		}
		a.up += dUp
		a.down += dDown
		a.last = nowMs
	}
	// 清理已消失的连接（避免内存增长）
	for id := range state {
		if !seen[id] {
			delete(state, id)
		}
	}
	mihomoConnMu.Unlock()

	for uuid, a := range perUser {
		_, _ = h.db.ExecContext(r.Context(),
			`INSERT INTO mihomo_traffic (uuid, up_total, down_total, last_active_at, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE up_total = up_total + VALUES(up_total),
			   down_total = down_total + VALUES(down_total), last_active_at = VALUES(last_active_at), updated_at = VALUES(updated_at)`,
			uuid, a.up, a.down, a.last, nowMs)
		_, _ = h.db.ExecContext(r.Context(),
			`INSERT INTO mihomo_traffic_hourly (uuid, node_id, hour_ts, up, down)
			 VALUES (?, ?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE up = up + VALUES(up), down = down + VALUES(down)`,
			uuid, nodeID, hourTs, a.up, a.down)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "users": len(perUser)})
}

// GET /api/mihomo/traffic/summary  按用户汇总（管理端）
func (h *handlers) mihomoTrafficSummary(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).UnixMilli()
	weekStart := dayStart - 6*86400000
	onlineSince := now.Add(-2 * time.Minute).UnixMilli()
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT t.uuid, t.up_total, t.down_total, t.last_active_at,
		        COALESCE(SUM(CASE WHEN h.hour_ts >= ? THEN h.up ELSE 0 END),0) AS today_up,
		        COALESCE(SUM(CASE WHEN h.hour_ts >= ? THEN h.down ELSE 0 END),0) AS today_down,
		        COALESCE(SUM(CASE WHEN h.hour_ts >= ? THEN h.up ELSE 0 END),0) AS week_up,
		        COALESCE(SUM(CASE WHEN h.hour_ts >= ? THEN h.down ELSE 0 END),0) AS week_down,
		        COALESCE((SELECT owner FROM uuid_info WHERE uuid = t.uuid LIMIT 1),'') AS owner,
		        COALESCE((SELECT remark FROM uuid_info WHERE uuid = t.uuid LIMIT 1),'') AS remark
		   FROM mihomo_traffic t
		   LEFT JOIN mihomo_traffic_hourly h ON h.uuid = t.uuid
		  GROUP BY t.uuid, t.up_total, t.down_total, t.last_active_at
		  ORDER BY t.last_active_at DESC
		  LIMIT 200`, dayStart, dayStart, weekStart, weekStart)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	for _, row := range rows {
		row["online"] = mapInt(row, "last_active_at", 0) >= onlineSince
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}
