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
		        COALESCE(sb.domain,'') AS sb_domain, COALESCE(sb.last_apply_at,0) AS sb_last_apply,
		        mh.id AS mh_id, mh.name AS mh_name, mh.template_id AS mh_tpl, COALESCE(mt.name,'') AS mh_tpl_name,
		        COALESCE(mt.mode,'subscription') AS mh_mode, COALESCE(mh.uuid_list,'[]') AS mh_uuid_list, mh.enabled AS mh_enabled,
		        COALESCE(mh.domain,'') AS mh_domain, COALESCE(mh.last_apply_at,0) AS mh_last_apply,
		        mp.id AS mp_id, mp.name AS mp_name, mp.template_id AS mp_tpl, COALESCE(pt.name,'') AS mp_tpl_name,
		        mp.enabled AS mp_enabled, COALESCE(mp.domain,'') AS mp_domain, COALESCE(mp.last_apply_at,0) AS mp_last_apply,
		        (SELECT COUNT(*) FROM mtproto_clients c WHERE c.node_id = mp.id AND c.status = 'active') AS mp_uuids
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
		name := mapStr(row, "sb_name", "")
		if name == "" {
			name = mapStr(row, "mh_name", "")
		}
		if name == "" {
			name = mapStr(row, "mp_name", "")
		}
		if name == "" {
			name = mapStr(row, "server_name", "")
		}
		row["node_name"] = name
		domain := mapStr(row, "sb_domain", "")
		if domain == "" {
			domain = mapStr(row, "mh_domain", "")
		}
		if domain == "" {
			domain = mapStr(row, "mp_domain", "")
		}
		row["domain"] = domain
		last := mapInt(row, "sb_last_apply", 0)
		if v := mapInt(row, "mh_last_apply", 0); v > last {
			last = v
		}
		if v := mapInt(row, "mp_last_apply", 0); v > last {
			last = v
		}
		row["last_apply_at"] = last
		row["services"] = func() int {
			n := 0
			if mapInt(row, "sb_id", 0) > 0 {
				n++
			}
			if mapInt(row, "mh_id", 0) > 0 {
				n++
			}
			if mapInt(row, "mp_id", 0) > 0 {
				n++
			}
			return n
		}()
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": rows})
}
