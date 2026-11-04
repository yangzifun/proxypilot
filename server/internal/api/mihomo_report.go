package api

import (
	"net/http"
	"strings"
)

// POST /api/mihomo/report?token=<uuid>
// 客户端（mihomo external-controller / 脚本）上报累计流量与连接数。
func (h *handlers) mihomoReport(w http.ResponseWriter, r *http.Request) {
	uuid := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("token")))
	if uuid == "" {
		uuid = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("uuid")))
	}
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "缺少或无效的 token")
		return
	}
	var n int
	_ = h.db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM (SELECT uuid FROM uuid_info UNION SELECT uuid FROM configs) t WHERE uuid = ?`, uuid).Scan(&n)
	if n == 0 {
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM singbox_templates WHERE users_json LIKE CONCAT('%', ?, '%')`, uuid).Scan(&n)
	}
	if n == 0 {
		writeError(w, http.StatusForbidden, "UUID 未授权")
		return
	}
	var body struct {
		UpTotal     int64  `json:"up_total"`
		DownTotal   int64  `json:"down_total"`
		Up          int64  `json:"up"`
		Down        int64  `json:"down"`
		Connections int64  `json:"connections"`
		Source      string `json:"source"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	up := body.UpTotal
	down := body.DownTotal
	if up == 0 && body.Up > 0 {
		up = body.Up
	}
	if down == 0 && body.Down > 0 {
		down = body.Down
	}
	ip := clientIP(r)
	ua := truncateRunes(r.UserAgent(), 200)
	_, err := h.db.ExecContext(r.Context(),
		`INSERT INTO mihomo_reports (uuid, up_total, down_total, connections, ip, ua, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE up_total = VALUES(up_total), down_total = VALUES(down_total),
		   connections = VALUES(connections), ip = VALUES(ip), ua = VALUES(ua), updated_at = VALUES(updated_at)`,
		uuid, up, down, body.Connections, ip, ua, nowMillis())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "保存上报失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// GET /api/mihomo/reports
func (h *handlers) mihomoReportsList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT rep.uuid, rep.up_total, rep.down_total, rep.connections, COALESCE(rep.ip,'') AS ip,
		        COALESCE(rep.ua,'') AS ua, rep.updated_at,
		        COALESCE((SELECT owner FROM uuid_info WHERE uuid = rep.uuid LIMIT 1),'') AS owner,
		        COALESCE((SELECT remark FROM uuid_info WHERE uuid = rep.uuid LIMIT 1),'') AS remark
		   FROM mihomo_reports rep
		  ORDER BY rep.updated_at DESC
		  LIMIT 200`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询上报记录失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}
