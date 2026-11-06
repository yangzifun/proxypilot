package api

import (
	"net/http"
)

// statsProtocols 返回各协议的用户数分布（按 configs.protocol 去重 uuid 统计）。
func (h *handlers) statsProtocols(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT protocol, COUNT(DISTINCT uuid) AS cnt FROM configs GROUP BY protocol ORDER BY cnt DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	var total int64
	for _, row := range rows {
		n := mapInt(row, "cnt", 0)
		total += n
		out = append(out, map[string]any{"protocol": mapStr(row, "protocol", "unknown"), "count": n})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "total": total, "data": out})
}