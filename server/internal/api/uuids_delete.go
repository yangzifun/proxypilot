package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
)

// DELETE /api/uuids  body: {"uuid": "..."}
// 删除分组本体与其配置、外部订阅源、MTProto 授权。
// 注意：sing-box 节点里的授权列表需要渲染后下发，这里只做数据库清理，
// 与 Worker 的 removeUuidFromNodes 相比少了“立即重渲染并下发”，由巡检兜底。
func (h *handlers) uuidsDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuid := strings.TrimSpace(req.UUID)
	if !uuidRe.MatchString(uuid) {
		writeError(w, http.StatusBadRequest, "UUID格式无效")
		return
	}

	tx, err := h.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	defer tx.Rollback()

	var deletedConfigs, deletedInfo int64
	if res, err := tx.ExecContext(r.Context(), `DELETE FROM configs WHERE uuid = ?`, uuid); err == nil {
		deletedConfigs, _ = res.RowsAffected()
	} else {
		h.log.Error("删除分组配置失败", "err", err)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	if res, err := tx.ExecContext(r.Context(), `DELETE FROM uuid_info WHERE uuid = ?`, uuid); err == nil {
		deletedInfo, _ = res.RowsAffected()
	} else if !errors.Is(err, sql.ErrNoRows) {
		h.log.Error("删除分组元信息失败", "err", err)
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}
	// 关联数据：外部订阅源、MTProto 授权（表不存在时忽略）
	_, _ = tx.ExecContext(r.Context(), `DELETE FROM uuid_sub_sources WHERE uuid = ?`, uuid)
	_, _ = tx.ExecContext(r.Context(), `DELETE FROM mtproto_clients WHERE uuid = ?`, uuid)

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "删除失败")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":         true,
		"deleted_configs": deletedConfigs,
		"deleted_info":    deletedInfo,
		"nodes_updated":   0,
		"nodes_deployed":  0,
		"message":         "已删除该分组的全部配置与元信息",
		"warning":         "节点上的授权列表需由 UUID 巡检或旧 Worker 重渲染后才完全移除",
	})
}
