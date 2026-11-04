package api

import (
	"net/http"
)

// GET /api/nodes/bindings
// 按服务器汇总三类服务（sing-box / mihomo / MTProto）的绑定情况，供统一「节点绑定」页使用。
func (h *handlers) nodeBindingsList(w http.ResponseWriter, r *http.Request) {
	rows, err := queryRowsAsMaps(r.Context(), h.db,
		`SELECT s.id AS server_id, s.name AS server_name, COALESCE(s.host,'') AS host,
		        COALESCE(s.last_status,'') AS last_status,
		        sb.id AS sb_id, sb.name AS sb_name, sb.template_id AS sb_tpl, COALESCE(st.name,'') AS sb_tpl_name,
		        COALESCE(sb.uuid_list,'[]') AS sb_uuid_list, sb.enabled AS sb_enabled,
		        mh.id AS mh_id, mh.name AS mh_name, mh.template_id AS mh_tpl, COALESCE(mt.name,'') AS mh_tpl_name,
		        COALESCE(mt.mode,'subscription') AS mh_mode, COALESCE(mh.uuid_list,'[]') AS mh_uuid_list, mh.enabled AS mh_enabled,
		        mp.id AS mp_id, mp.name AS mp_name, mp.template_id AS mp_tpl, COALESCE(pt.name,'') AS mp_tpl_name,
		        mp.enabled AS mp_enabled
		   FROM servers s
		   LEFT JOIN singbox_nodes sb ON sb.server_id = s.id
		   LEFT JOIN singbox_templates st ON st.id = sb.template_id
		   LEFT JOIN mihomo_nodes mh ON mh.server_id = s.id
		   LEFT JOIN mihomo_templates mt ON mt.id = mh.template_id
		   LEFT JOIN mtproto_nodes mp ON mp.server_id = s.id
		   LEFT JOIN mtproto_templates pt ON pt.id = mp.template_id
		  ORDER BY s.id ASC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询节点绑定失败")
		return
	}
	for _, row := range rows {
		row["sb_uuids"] = len(mihomoParseUUIDs(mapStr(row, "sb_uuid_list", "[]")))
		row["mh_uuids"] = len(mihomoParseUUIDs(mapStr(row, "mh_uuid_list", "[]")))
		delete(row, "sb_uuid_list")
		delete(row, "mh_uuid_list")
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}
