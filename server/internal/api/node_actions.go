package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type nodeSetDiff struct {
	ServerID   int64    `json:"server_id"`
	ServerName string   `json:"server_name"`
	MissingSB  []string `json:"missing_sb"`
	MissingMH  []string `json:"missing_mh"`
	MissingMP  []string `json:"missing_mp"`
	ExtraSB    []string `json:"extra_sb"`
	ExtraMH    []string `json:"extra_mh"`
	ExtraMP    []string `json:"extra_mp"`
}

// POST /api/nodes/{server_id}/apply  对服务器上已绑定的全部服务执行下发
func (h *handlers) nodesApplyServer(w http.ResponseWriter, r *http.Request) {
	sid, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	results := []map[string]any{}
	var sbID, mhID, mpID int64
	_ = h.db.QueryRowContext(r.Context(), "SELECT id FROM singbox_nodes WHERE server_id = ? LIMIT 1", sid).Scan(&sbID)
	_ = h.db.QueryRowContext(r.Context(), "SELECT id FROM mihomo_nodes WHERE server_id = ? LIMIT 1", sid).Scan(&mhID)
	_ = h.db.QueryRowContext(r.Context(), "SELECT id FROM mtproto_nodes WHERE server_id = ? LIMIT 1", sid).Scan(&mpID)

	if sbID > 0 {
		_, _, _, _, msg := h.applySingboxNode(r.Context(), sbID, nil, false)
		okv := msg == ""
		item := map[string]any{"kind": "singbox", "ok": okv}
		if msg != "" {
			item["error"] = msg
		}
		results = append(results, item)
	}
	if mhID > 0 {
		if taskID, msg := h.queueMihomoApply(r.Context(), r, mhID); msg != "" {
			results = append(results, map[string]any{"kind": "mihomo", "ok": false, "error": msg})
		} else {
			results = append(results, map[string]any{"kind": "mihomo", "ok": true, "task_id": taskID})
		}
	}
	if mpID > 0 {
		_, _, msg := h.applyMtprotoToNode(r.Context(), mpID)
		okv := msg == ""
		item := map[string]any{"kind": "mtproto", "ok": okv}
		if msg != "" {
			item["error"] = msg
		}
		results = append(results, item)
	}
	if len(results) == 0 {
		writeError(w, http.StatusBadRequest, "该服务器尚未绑定任何服务")
		return
	}
	_, _ = h.db.ExecContext(r.Context(), "UPDATE singbox_nodes SET last_apply_at = ? WHERE server_id = ? AND last_apply_at = 0", nowMillis(), sid)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "results": results})
}

// GET /api/nodes/check  UUID 授权一致性检查（三服务互相对比）
func (h *handlers) nodesCheck(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT s.id AS server_id, s.name AS server_name,
		        COALESCE(sb.id,0) AS sb_id, COALESCE(sb.uuid_list,'[]') AS sb_list,
		        COALESCE(mh.id,0) AS mh_id, COALESCE(mh.uuid_list,'[]') AS mh_list,
		        (SELECT COUNT(*) FROM mtproto_nodes mp WHERE mp.server_id = s.id) AS mp_exists
		   FROM servers s
		   LEFT JOIN singbox_nodes sb ON sb.server_id = s.id
		   LEFT JOIN mihomo_nodes mh ON mh.server_id = s.id
		  ORDER BY s.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	diffs := []nodeSetDiff{}
	for _, row := range rows {
		sid := mapInt(row, "server_id", 0)
		sb := map[string]bool{}
		for _, u := range mihomoParseUUIDs(mapStr(row, "sb_list", "[]")) {
			sb[strings.ToLower(u)] = true
		}
		mh := map[string]bool{}
		for _, u := range mihomoParseUUIDs(mapStr(row, "mh_list", "[]")) {
			mh[strings.ToLower(u)] = true
		}
		mp := map[string]bool{}
		mpRows, _ := queryRowsAsMaps(r.Context(), h.db,
			"SELECT LOWER(c.uuid) AS uuid FROM mtproto_clients c JOIN mtproto_nodes n ON n.id = c.node_id WHERE n.server_id = ? AND c.status = 'active'", sid)
		for _, mr := range mpRows {
			mp[mapStr(mr, "uuid", "")] = true
		}
		union := map[string]bool{}
		for k := range sb {
			union[k] = true
		}
		for k := range mh {
			union[k] = true
		}
		for k := range mp {
			union[k] = true
		}
		d := nodeSetDiff{ServerID: int64(sid), ServerName: mapStr(row, "server_name", "")}
		hasSB := mapInt(row, "sb_id", 0) > 0
		hasMH := mapInt(row, "mh_id", 0) > 0
		hasMP := mapInt(row, "mp_exists", 0) > 0
		for u := range union {
			if hasSB && !sb[u] {
				d.MissingSB = append(d.MissingSB, u)
			}
			if hasMH && !mh[u] {
				d.MissingMH = append(d.MissingMH, u)
			}
			if hasMP && !mp[u] {
				d.MissingMP = append(d.MissingMP, u)
			}
		}
		if len(d.MissingSB)+len(d.MissingMH)+len(d.MissingMP) > 0 {
			diffs = append(diffs, d)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": diffs, "issue_count": len(diffs)})
}

// POST /api/nodes/check/fix  以三者并集为准，补齐 sing-box / mihomo 的 uuid_list
func (h *handlers) nodesCheckFix(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT s.id AS server_id,
		        COALESCE(sb.id,0) AS sb_id, COALESCE(sb.uuid_list,'[]') AS sb_list,
		        COALESCE(mh.id,0) AS mh_id, COALESCE(mh.uuid_list,'[]') AS mh_list
		   FROM servers s
		   LEFT JOIN singbox_nodes sb ON sb.server_id = s.id
		   LEFT JOIN mihomo_nodes mh ON mh.server_id = s.id
		  ORDER BY s.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询失败")
		return
	}
	fixed := 0
	for _, row := range rows {
		sid := mapInt(row, "server_id", 0)
		union := map[string]bool{}
		for _, u := range mihomoParseUUIDs(mapStr(row, "sb_list", "[]")) {
			union[strings.ToLower(u)] = true
		}
		for _, u := range mihomoParseUUIDs(mapStr(row, "mh_list", "[]")) {
			union[strings.ToLower(u)] = true
		}
		mpRows, _ := queryRowsAsMaps(r.Context(), h.db,
			"SELECT LOWER(c.uuid) AS uuid FROM mtproto_clients c JOIN mtproto_nodes n ON n.id = c.node_id WHERE n.server_id = ? AND c.status = 'active'", sid)
		for _, mr := range mpRows {
			union[mapStr(mr, "uuid", "")] = true
		}
		all := make([]string, 0, len(union))
		for u := range union {
			if u != "" {
				all = append(all, u)
			}
		}
		raw, _ := json.Marshal(all)
		if id := mapInt(row, "sb_id", 0); id > 0 {
			if _, err := h.db.ExecContext(r.Context(), "UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?", string(raw), nowMillis(), id); err == nil {
				fixed++
			}
		}
		if id := mapInt(row, "mh_id", 0); id > 0 {
			if _, err := h.db.ExecContext(r.Context(), "UPDATE mihomo_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?", string(raw), nowMillis(), id); err == nil {
				fixed++
			}
		}
	}
	h.mihomoAudit(r, "nodes-check-fix", 0, fmt.Sprintf("UUID 一致性修复 %d 项", fixed))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "fixed": fixed})
}
