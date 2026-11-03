package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
)

type configRow struct {
	ID            int64  `json:"id"`
	UUID          string `json:"uuid"`
	ConfigData    string `json:"config_data"`
	Protocol      string `json:"protocol"`
	Remark        string `json:"remark"`
	DomainHosting string `json:"domain_hosting"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
	SingboxNodeID int64  `json:"singbox_node_id"`
}

// uuidExistsForManagement：uuid_info 或 configs 任一存在即视为存在（与 Worker 一致）。
func (h *handlers) uuidExistsForManagement(ctx context.Context, uuid string) bool {
	var n int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM uuid_info WHERE uuid = ?`, uuid).Scan(&n); err == nil && n > 0 {
		return true
	}
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM configs WHERE uuid = ?`, uuid).Scan(&n); err == nil && n > 0 {
		return true
	}
	return false
}

// insertConfigLines 按行去重后插入；返回新增条数。
func (h *handlers) insertConfigLines(ctx context.Context, uuid, text string) (int, error) {
	lines := strings.Split(text, "\n")
	seen := map[string]bool{}
	rows, err := h.db.QueryContext(ctx, `SELECT config_data FROM configs WHERE uuid = ?`, uuid)
	if err == nil {
		for rows.Next() {
			var s sql.NullString
			if rows.Scan(&s) == nil && s.Valid {
				seen[s.String] = true
			}
		}
		rows.Close()
	}

	var valid []configRow
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		p := getProtocol(line)
		if p == "unknown" {
			continue
		}
		seen[line] = true
		valid = append(valid, configRow{
			UUID: uuid, ConfigData: line, Protocol: p,
			Remark: extractRemark(line, p),
		})
	}
	if len(valid) == 0 {
		return 0, nil
	}

	tx, err := h.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := nowMillis()
	for _, c := range valid {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO configs (uuid, config_data, protocol, remark, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			c.UUID, c.ConfigData, c.Protocol, nullIfEmpty(c.Remark), now, now); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(valid), nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func (h *handlers) queryConfigs(ctx context.Context, uuid, protocol, q string, page, size int) ([]configRow, int64, error) {
	conds := []string{"uuid = ?", "config_data IS NOT NULL", "config_data <> ''"}
	args := []any{uuid}
	if protocol != "" && protocol != "all" {
		conds = append(conds, "protocol = ?")
		args = append(args, protocol)
	}
	if q = strings.TrimSpace(q); q != "" {
		conds = append(conds, "(IFNULL(remark,'') LIKE ? OR IFNULL(config_data,'') LIKE ?)")
		like := "%" + q + "%"
		args = append(args, like, like)
	}
	where := " WHERE " + strings.Join(conds, " AND ")

	var total int64
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM configs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	rows, err := h.db.QueryContext(ctx,
		`SELECT id, uuid, config_data, COALESCE(protocol,''), COALESCE(remark,''),
		        COALESCE(domain_hosting,''), COALESCE(created_at,0), COALESCE(updated_at,0),
		        COALESCE(singbox_node_id,0)
		   FROM configs`+where+` ORDER BY id ASC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []configRow{}
	for rows.Next() {
		var c configRow
		if err := rows.Scan(&c.ID, &c.UUID, &c.ConfigData, &c.Protocol, &c.Remark,
			&c.DomainHosting, &c.CreatedAt, &c.UpdatedAt, &c.SingboxNodeID); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// GET/POST/PUT/DELETE /api/uuid-configs（后台 + API 密钥）
func (h *handlers) uuidConfigsDispatch(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.uuidConfigsList(w, r)
	case http.MethodPost:
		h.uuidConfigsAdd(w, r)
	case http.MethodPut:
		h.uuidConfigsUpdate(w, r)
	case http.MethodDelete:
		h.uuidConfigsDelete(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func (h *handlers) uuidConfigsList(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	page := atoiDefault(r.URL.Query().Get("page"), 1)
	size := atoiDefault(r.URL.Query().Get("size"), 10)
	data, total, err := h.queryConfigs(r.Context(), uuid,
		r.URL.Query().Get("protocol"), r.URL.Query().Get("q"), page, size)
	if err != nil {
		h.log.Error("查询配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询配置失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"data":    data,
		"total":   total,
	})
}

func (h *handlers) uuidConfigsAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID       string `json:"uuid"`
		ConfigData string `json:"config_data"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "字段缺失")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}
	if strings.TrimSpace(req.ConfigData) == "" {
		writeError(w, http.StatusBadRequest, "字段缺失")
		return
	}
	if !h.uuidExistsForManagement(r.Context(), uuid) && !h.hasAPIGroupWrite(r) {
		writeError(w, http.StatusForbidden, "API 密钥权限不足（需要 groups:write 才能为新 UUID 添加配置）")
		return
	}
	n, err := h.insertConfigLines(r.Context(), uuid, req.ConfigData)
	if err != nil {
		h.log.Error("写入配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "写入配置失败")
		return
	}
	if n == 0 {
		writeError(w, http.StatusBadRequest, "无有效配置")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "inserted": n})
}

func (h *handlers) uuidConfigsUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID         int64  `json:"id"`
		ConfigData string `json:"config_data"`
	}
	if err := decodeJSON(w, r, &req); err != nil || req.ID <= 0 {
		writeError(w, http.StatusBadRequest, "缺少配置ID")
		return
	}
	text := strings.TrimSpace(req.ConfigData)
	protocol := getProtocol(text)
	if protocol == "unknown" {
		writeError(w, http.StatusBadRequest, "不支持的配置格式")
		return
	}
	res, err := h.db.ExecContext(r.Context(),
		`UPDATE configs SET config_data = ?, protocol = ?, remark = ?, updated_at = ? WHERE id = ?`,
		text, protocol, nullIfEmpty(extractRemark(text, protocol)), nowMillis(), req.ID)
	if err != nil {
		h.log.Error("更新配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "更新配置失败")
		return
	}
	n, _ := res.RowsAffected()
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "message": map[bool]string{true: "Updated", false: "No changes made"}[n > 0],
		"rows_affected": n,
	})
}

func (h *handlers) uuidConfigsDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID   *int64  `json:"id"`
		IDs  []int64 `json:"ids"`
		UUID string  `json:"uuid"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	if len(body.IDs) > 0 {
		ids := body.IDs
		if len(ids) > 1000 {
			ids = ids[:1000]
		}
		targets := configTargetsByIDs(r.Context(), h.db, ids)
		n, err := deleteConfigsByIDs(r.Context(), h.db, ids)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
		revoked, deployed := h.revokeEmptyConfigTargets(r.Context(), targets)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "deleted": n, "nodes_revoked": revoked, "nodes_deployed": deployed,
		})
		return
	}
	if body.ID != nil && *body.ID > 0 {
		targets := configTargetsByIDs(r.Context(), h.db, []int64{*body.ID})
		res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE id = ?`, *body.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
		n, _ := res.RowsAffected()
		revoked, deployed := h.revokeEmptyConfigTargets(r.Context(), targets)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "deleted": n, "nodes_revoked": revoked, "nodes_deployed": deployed,
		})
		return
	}
	target := strings.TrimSpace(body.UUID)
	if target != "" {
		if !uuidRe.MatchString(target) {
			writeError(w, http.StatusBadRequest, "UUID格式无效")
			return
		}
		var before int64
		_ = h.db.QueryRowContext(r.Context(),
			`SELECT COUNT(*) FROM configs WHERE uuid = ?`, target).Scan(&before)
		res, err := h.db.ExecContext(r.Context(), `DELETE FROM configs WHERE uuid = ?`, target)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "删除失败")
			return
		}
		n, _ := res.RowsAffected()
		if n < before {
			n = before
		}
		// 一键清理 = 同时解除所有节点对该 UUID 的授权，并立即重渲染下发，
		// 否则节点 uuid_list 仍保留该 UUID，重新授权时会被判定为「无变化」而不下发。
		revoked, deployed := h.revokeUUIDFromNodes(r.Context(), target)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "deleted": n,
			"nodes_revoked": revoked, "nodes_deployed": deployed,
		})
		return
	}
	writeError(w, http.StatusBadRequest, "缺少 id / ids / uuid")
}

// revokeUUIDFromNodes 从所有 sing-box 节点的 uuid_list 移除该 UUID，并立即重渲染下发。
// 返回（解除授权的节点数, 成功下发的节点数）。
func (h *handlers) revokeUUIDFromNodes(ctx context.Context, uuid string) (int, int) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, COALESCE(uuid_list,'[]') AS uuid_list FROM singbox_nodes`)
	if err != nil {
		return 0, 0
	}
	affected := make([]int64, 0, len(rows))
	nextByNode := map[int64][]string{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		cur := parseUUIDList(mapStr(row, "uuid_list", "[]"))
		if id == 0 || !containsString(cur, uuid) {
			continue
		}
		next := make([]string, 0, len(cur))
		for _, u := range cur {
			if !strings.EqualFold(strings.TrimSpace(u), uuid) {
				next = append(next, u)
			}
		}
		raw, err := json.Marshal(next)
		if err != nil {
			continue
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, raw, nowMillis(), id); err == nil {
			affected = append(affected, id)
			nextByNode[id] = next
		}
	}
	deployed := 0
	for _, id := range affected {
		if _, _, _, _, msg := h.applySingboxNode(ctx, id, nil, false); msg != "" {
			continue
		}
		deployed++
		// 节点侧用户列表变化后同步回收 subscriptions 里该节点的配置
		if tpl, node, _, _, msg := h.loadSingboxNodeFull(ctx, id); msg == "" {
			users := singboxUsersFromUUIDs(tpl, nextByNode[id])
			_, _, _ = h.syncSingboxConfigsForUsers(ctx, id, node.Name, tpl, node, users)
		}
	}
	return len(affected), deployed
}

// configTarget 一条订阅配置所属的 (uuid, 节点)。
type configTarget struct {
	UUID   string
	NodeID int64
}

// configTargetsByIDs 删除前先记录这些配置属于哪个 UUID / 节点，便于删除后解除授权。
func configTargetsByIDs(ctx context.Context, db *sql.DB, ids []int64) []configTarget {
	if len(ids) == 0 {
		return nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := db.QueryContext(ctx,
		`SELECT COALESCE(uuid,''), COALESCE(singbox_node_id,0) FROM configs WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []configTarget{}
	for rows.Next() {
		var t configTarget
		if rows.Scan(&t.UUID, &t.NodeID) == nil && t.UUID != "" {
			out = append(out, t)
		}
	}
	return out
}

// revokeEmptyConfigTargets 某个 UUID 在某节点上已无任何配置时，解除该节点授权并重渲染下发。
func (h *handlers) revokeEmptyConfigTargets(ctx context.Context, targets []configTarget) (int, int) {
	seen := map[string]bool{}
	revoked, deployed := 0, 0
	for _, t := range targets {
		if t.NodeID <= 0 || strings.TrimSpace(t.UUID) == "" {
			continue
		}
		key := itoa64(t.NodeID) + "|" + strings.ToLower(strings.TrimSpace(t.UUID))
		if seen[key] {
			continue
		}
		seen[key] = true
		var left int64
		_ = h.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM configs WHERE uuid = ? AND singbox_node_id = ?`, t.UUID, t.NodeID).Scan(&left)
		if left > 0 {
			continue
		}
		ok, applied := h.revokeUUIDFromNode(ctx, t.NodeID, t.UUID)
		if ok {
			revoked++
			if applied {
				deployed++
			}
		}
	}
	return revoked, deployed
}

// revokeUUIDFromNode 从指定节点的 uuid_list 移除 UUID，并重渲染/下发 + 回收该 UUID 的订阅配置。
func (h *handlers) revokeUUIDFromNode(ctx context.Context, nodeID int64, uuid string) (bool, bool) {
	var raw string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(uuid_list,'[]') FROM singbox_nodes WHERE id = ? LIMIT 1`, nodeID).Scan(&raw); err != nil {
		return false, false
	}
	cur := parseUUIDList(raw)
	if !containsUUIDFold(cur, uuid) {
		return false, false
	}
	next := make([]string, 0, len(cur))
	for _, u := range cur {
		if !strings.EqualFold(strings.TrimSpace(u), strings.TrimSpace(uuid)) {
			next = append(next, u)
		}
	}
	payload, err := json.Marshal(next)
	if err != nil {
		return false, false
	}
	if _, err := h.db.ExecContext(ctx,
		`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, payload, nowMillis(), nodeID); err != nil {
		return false, false
	}
	applied := false
	if _, _, _, _, msg := h.applySingboxNode(ctx, nodeID, nil, false); msg == "" {
		applied = true
	}
	if tpl, node, _, _, m := h.loadSingboxNodeFull(ctx, nodeID); m == "" {
		users := singboxUsersFromUUIDs(tpl, next)
		_, _, _ = h.syncSingboxConfigsForUsers(ctx, nodeID, node.Name, tpl, node, users)
	}
	h.verifyNodeConsistencyAsync(nodeID)
	return true, applied
}

func deleteConfigsByIDs(ctx context.Context, db *sql.DB, ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := db.ExecContext(ctx, `DELETE FROM configs WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
