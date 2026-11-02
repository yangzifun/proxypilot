package api

import (
	"database/sql"
	"net/http"
	"strings"
)

// GET /manage/uuid-info/{uuid}、GET /manage/uuid-info?uuid= —— 用户端 UUID 信息。
func (h *handlers) manageUUIDInfoGet(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" {
		uuid = strings.TrimSpace(r.URL.Query().Get("uuid"))
	}
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "缺少 uuid")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	data, err := h.queryUUIDInfo(r, uuid)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "exists": false, "data": map[string]any{"uuid": uuid},
		})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取用户信息失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "exists": true, "data": data})
}

func (h *handlers) queryUUIDInfo(r *http.Request, uuid string) (map[string]any, error) {
	var owner, email, contact, remark, status string
	var createdAt, updatedAt int64
	err := h.db.QueryRowContext(r.Context(), `SELECT
	      COALESCE(owner,''), COALESCE(email,''), COALESCE(contact_info,''), COALESCE(remark,''),
	      COALESCE(status,''), COALESCE(created_at,0), COALESCE(updated_at,0)
	    FROM uuid_info WHERE uuid = ? LIMIT 1`, uuid).
		Scan(&owner, &email, &contact, &remark, &status, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"uuid": uuid, "owner": owner, "email": email, "status": status,
		"contact_info": contact, "remark": remark,
		"created_at": createdAt, "updated_at": updatedAt,
	}, nil
}

// POST /manage/uuid-info —— 用户端只允许改联系方式 / 备注；所有者和邮箱由管理员或换绑流程维护。
func (h *handlers) manageUUIDInfoSave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UUID        string `json:"uuid"`
		ContactInfo string `json:"contact_info"`
		Remark      string `json:"remark"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "请求体无效")
		return
	}
	uuid := strings.TrimSpace(body.UUID)
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "缺少 uuid")
		return
	}
	if !h.requireManage(w, r, uuid) {
		return
	}
	var exists int
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM uuid_info WHERE uuid = ?`, uuid).Scan(&exists); err != nil {
		writeError(w, http.StatusInternalServerError, "读取用户信息失败")
		return
	}
	if exists == 0 {
		writeError(w, http.StatusNotFound, "UUID 不存在")
		return
	}
	if _, err := h.db.ExecContext(r.Context(), `UPDATE uuid_info SET contact_info = ?, remark = ?, updated_at = ?
	    WHERE uuid = ?`, truncateRunes(body.ContactInfo, 2000), truncateRunes(body.Remark, 2000), nowMillis(), uuid); err != nil {
		writeError(w, http.StatusInternalServerError, "保存用户信息失败")
		return
	}
	data, _ := h.queryUUIDInfo(r, uuid)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}
