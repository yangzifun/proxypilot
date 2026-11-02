package api

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

const uuidGroupsCTE = `WITH uuid_grp AS (
  SELECT ui.uuid AS uuid, ui.owner AS owner, ui.email AS email, ui.contact_info AS contact_info,
         IFNULL(ui.status,'active') AS status, ui.remark AS remark,
         IFNULL(ui.access_count,0) AS access_count, IFNULL(ui.last_accessed,0) AS last_accessed,
         ui.created_at AS created_at, IFNULL(ui.updated_at,0) AS updated_at
    FROM uuid_info ui
  UNION ALL
  SELECT c.uuid, '', '', '', 'active', '', 0, 0,
         MIN(c.created_at), IFNULL(MAX(c.updated_at), MIN(c.created_at))
    FROM configs c
   WHERE c.uuid NOT IN (SELECT uuid FROM uuid_info)
   GROUP BY c.uuid
)`

type uuidRow struct {
	UUID        string `json:"uuid"`
	Owner       string `json:"owner"`
	Email       string `json:"email"`
	ContactInfo string `json:"contact_info"`
	Status      string `json:"status"`
	Remark      string `json:"remark"`
	AccessCount int64  `json:"access_count"`
	LastAccess  int64  `json:"last_accessed"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
	ConfigCount int64  `json:"config_count"`
	Count       int64  `json:"count"`
	NodeCount   int64  `json:"node_count"`
}

// GET /api/uuids
func (h *handlers) uuidsList(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := strings.TrimSpace(qs.Get("q"))
	status := strings.ToLower(strings.TrimSpace(qs.Get("status")))
	all := qs.Get("all") == "1" || strings.EqualFold(qs.Get("all"), "true")
	page := atoiDefault(qs.Get("page"), 1)
	size := atoiDefault(qs.Get("size"), 10)
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 10
	}
	if size > 1000 {
		size = 1000
	}

	orderCol := map[string]string{
		"uuid": "t.uuid", "owner": "t.owner", "email": "t.email",
		"status": "t.status", "count": "config_count", "updated_at": "t.updated_at",
	}[qs.Get("sort")]
	if orderCol == "" {
		orderCol = "t.updated_at"
	}
	orderDir := "DESC"
	if strings.EqualFold(qs.Get("order"), "asc") {
		orderDir = "ASC"
	}

	var conds []string
	var args []any
	if q != "" {
		conds = append(conds, `(t.uuid LIKE ? OR IFNULL(t.owner,'') LIKE ? OR IFNULL(t.email,'') LIKE ? OR IFNULL(t.remark,'') LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like)
	}
	if status != "" {
		conds = append(conds, "IFNULL(t.status,'active') = ?")
		args = append(args, status)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	countSQL := uuidGroupsCTE + ` SELECT COUNT(*) FROM uuid_grp t` + where
	if err := h.db.QueryRowContext(r.Context(), countSQL, args...).Scan(&total); err != nil {
		h.log.Error("统计 UUID 失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询分组失败")
		return
	}

	listSQL := uuidGroupsCTE + ` SELECT t.uuid, COALESCE(t.owner,''), COALESCE(t.email,''), COALESCE(t.contact_info,''),
	  COALESCE(t.status,'active'), COALESCE(t.remark,''), COALESCE(t.access_count,0), COALESCE(t.last_accessed,0),
	  COALESCE(t.created_at,0), COALESCE(t.updated_at,0),
	  (SELECT COUNT(*) FROM configs c2 WHERE c2.uuid = t.uuid) AS config_count
	  FROM uuid_grp t` + where + ` ORDER BY ` + orderCol + ` ` + orderDir
	queryArgs := append([]any{}, args...)
	if !all {
		listSQL += " LIMIT ? OFFSET ?"
		queryArgs = append(queryArgs, size, (page-1)*size)
	}

	rows, err := h.db.QueryContext(r.Context(), listSQL, queryArgs...)
	if err != nil {
		h.log.Error("查询 UUID 失败", "err", err)
		writeError(w, http.StatusInternalServerError, "查询分组失败")
		return
	}
	defer rows.Close()

	data := make([]uuidRow, 0, size)
	for rows.Next() {
		var it uuidRow
		var statusVal sql.NullString
		if err := rows.Scan(&it.UUID, &it.Owner, &it.Email, &it.ContactInfo, &statusVal,
			&it.Remark, &it.AccessCount, &it.LastAccess, &it.CreatedAt, &it.UpdatedAt, &it.ConfigCount); err != nil {
			h.log.Error("扫描 UUID 失败", "err", err)
			writeError(w, http.StatusInternalServerError, "解析分组失败")
			return
		}
		if statusVal.Valid && statusVal.String != "" {
			it.Status = statusVal.String
		} else {
			it.Status = "active"
		}
		it.Count = it.ConfigCount
		it.NodeCount = it.ConfigCount
		data = append(data, it)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "读取分组失败")
		return
	}

	respSize := size
	if all {
		page = 1
		respSize = len(data)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"total":   total,
		"page":    page,
		"size":    respSize,
		"data":    data,
	})
}

type uuidSaveRequest struct {
	UUID        string  `json:"uuid"`
	Owner       *string `json:"owner"`
	Email       *string `json:"email"`
	ContactInfo *string `json:"contact_info"`
	Status      *string `json:"status"`
	Remark      *string `json:"remark"`
}

// POST/PUT /api/uuids
func (h *handlers) uuidsSave(w http.ResponseWriter, r *http.Request) {
	var req uuidSaveRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}

	var (
		curOwner, curEmail, curContact, curRemark sql.NullString
		curStatus                                 sql.NullString
	)
	err := h.db.QueryRowContext(r.Context(),
		`SELECT owner, email, contact_info, status, remark FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).
		Scan(&curOwner, &curEmail, &curContact, &curStatus, &curRemark)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "查询分组失败")
		return
	}

	pick := func(p *string, cur sql.NullString) string {
		if p != nil {
			return strings.TrimSpace(*p)
		}
		return cur.String
	}
	owner := pick(req.Owner, curOwner)
	email := pick(req.Email, curEmail)
	contact := pick(req.ContactInfo, curContact)
	remark := pick(req.Remark, curRemark)
	status := normalizeUuidStatus(curStatus.String)
	if req.Status != nil {
		status = normalizeUuidStatus(*req.Status)
	}

	if email != "" {
		if taken, found := h.emailTakenBy(r.Context(), email, uuid); found {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error":         "该邮箱已绑定 UUID " + taken + "：一个账号只能绑定一个 UUID",
				"code":          "email_taken",
				"existing_uuid": taken,
			})
			return
		}
	}

	now := nowMillis()
	if exists {
		if _, err := h.db.ExecContext(r.Context(),
			`UPDATE uuid_info SET owner = ?, email = ?, contact_info = ?, status = ?, remark = ?, updated_at = ?
			  WHERE uuid = ?`,
			owner, email, contact, status, remark, now, uuid); err != nil {
			h.log.Error("更新 UUID 失败", "err", err)
			writeError(w, http.StatusInternalServerError, "保存失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "created": false, "uuid": uuid})
		return
	}
	if _, err := h.db.ExecContext(r.Context(),
		`INSERT INTO uuid_info (uuid, owner, email, contact_info, status, remark, last_accessed, access_count, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, 0, ?, ?)`,
		uuid, owner, email, contact, status, remark, now, now); err != nil {
		h.log.Error("创建 UUID 失败", "err", err)
		writeError(w, http.StatusInternalServerError, "保存失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "created": true, "uuid": uuid})
}

func normalizeUuidStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "inactive", "suspended":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "active"
	}
}

func atoiDefault(raw string, fallback int) int {
	n := 0
	if raw == "" {
		return fallback
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}
