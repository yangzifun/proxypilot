package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// POST /api/mihomo/check/fix
// 自动修复绑定中的越权 UUID（该服务器 sing-box 授权列表之外的条目）。
func (h *handlers) mihomoCheckFix(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT n.id, n.name, n.server_id, COALESCE(n.uuid_list,'[]') AS uuid_list
		   FROM mihomo_nodes n ORDER BY n.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type fixItem struct {
		BindingID int64    `json:"binding_id"`
		Name      string   `json:"name"`
		Removed   []string `json:"removed"`
	}
	fixed := []fixItem{}
	skipped := []fixItem{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		name := mapStr(row, "name", "")
		list := mihomoParseUUIDs(mapStr(row, "uuid_list", "[]"))
		if len(list) == 0 {
			continue
		}
		var sbNodeID int64
		if h.db.QueryRowContext(r.Context(),
			`SELECT id FROM singbox_nodes WHERE server_id = ? ORDER BY enabled DESC, id ASC LIMIT 1`,
			mapInt(row, "server_id", 0)).Scan(&sbNodeID) != nil || sbNodeID == 0 {
			continue
		}
		var nodeUUIDRaw string
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(uuid_list,'[]') FROM singbox_nodes WHERE id = ?`, sbNodeID).Scan(&nodeUUIDRaw)
		allowed := map[string]bool{}
		for _, u := range mihomoParseUUIDs(nodeUUIDRaw) {
			allowed[strings.ToLower(u)] = true
		}
		if len(allowed) == 0 {
			continue
		}
		keep := make([]string, 0, len(list))
		removed := make([]string, 0)
		for _, u := range list {
			if allowed[strings.ToLower(u)] {
				keep = append(keep, u)
			} else {
				removed = append(removed, u)
			}
		}
		if len(removed) == 0 {
			continue
		}
		if len(keep) == 0 {
			/* 全部 UUID 都越权：清空会退化为「全部用户」，不自动处理 */
			skipped = append(skipped, fixItem{int64(id), name, removed})
			continue
		}
		raw, _ := json.Marshal(keep)
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE mihomo_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`,
			string(raw), nowMillis(), id); err != nil {
			continue
		}
		fixed = append(fixed, fixItem{int64(id), name, removed})
	}
	if len(fixed) > 0 {
		h.mihomoAudit(r, "mihomo-bind-checkfix", 0, fmt.Sprintf("自动修复 %d 个绑定的越权 UUID", len(fixed)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    map[string]any{"fixed": len(fixed), "items": fixed, "skipped": skipped},
	})
}
