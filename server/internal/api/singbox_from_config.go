package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type sbParsed struct {
	Users          []sbUser
	Ports          map[string]int
	Paths          map[string]string
	Enable         map[string]int
	SSMethod       string
	SSKey          string
	SSEnabled      bool
	HY2Up          int64
	HY2Down        int64
	Reality        map[string]string
	Warp           map[string]string
	HasWarp        bool
	Masque         bool
	MasquePort     int
	MasquePath     string
	MasqueVersions []int
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asSlice(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func asInt(v any) int64 {
	return int64(jsonNum(v))
}

func parseSbConfig(text string) (*sbParsed, string) {
	var cfg map[string]any
	if err := json.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, "sb.json 不是合法 JSON：" + err.Error()
	}
	inbounds := asSlice(cfg["inbounds"])
	if len(inbounds) == 0 {
		return nil, "sb.json 里没有 inbounds"
	}
	p := &sbParsed{
		Ports: map[string]int{}, Paths: map[string]string{},
		Enable:  map[string]int{"ss": 0, "anytls": 0, "hy2": 0, "reality": 0, "vlws": 0, "vmess": 0, "masque": 0},
		Reality: map[string]string{}, Warp: map[string]string{},
	}
	users := map[string]*sbUser{}
	order := []string{}
	pick := func(u map[string]any) *sbUser {
		key := asString(u["name"])
		if key == "" {
			key = asString(u["uuid"])
		}
		if key == "" {
			key = fmt.Sprintf("u%d", len(order))
		}
		cur, ok := users[key]
		if !ok {
			cur = &sbUser{Name: key}
			users[key] = cur
			order = append(order, key)
		}
		if v := asString(u["uuid"]); v != "" {
			cur.UUID = v
		}
		return cur
	}

	for _, raw := range inbounds {
		ib := asMap(raw)
		typ := asString(ib["type"])
		port := int(asInt(ib["listen_port"]))
		switch typ {
		case "shadowsocks":
			p.Enable["ss"] = 1
			p.SSEnabled = true
			p.Ports["ss"] = port
			p.SSMethod = asString(ib["method"])
			p.SSKey = asString(ib["password"])
			for _, u := range asSlice(ib["users"]) {
				um := asMap(u)
				c := pick(um)
				if pw := asString(um["password"]); pw != "" {
					c.SSPassword = pw
				}
			}
		case "anytls":
			p.Enable["anytls"] = 1
			p.Ports["anytls"] = port
			for _, u := range asSlice(ib["users"]) {
				um := asMap(u)
				c := pick(um)
				if c.UUID == "" {
					c.UUID = asString(um["password"])
				}
			}
		case "hysteria2":
			p.Enable["hy2"] = 1
			p.Ports["hy2"] = port
			p.HY2Up = asInt(ib["up_mbps"])
			p.HY2Down = asInt(ib["down_mbps"])
			for _, u := range asSlice(ib["users"]) {
				um := asMap(u)
				c := pick(um)
				if c.UUID == "" {
					c.UUID = asString(um["password"])
				}
			}
		case "tuic":
			p.Enable["vmess"] = 1
			p.Ports["vmess"] = port
			for _, u := range asSlice(ib["users"]) {
				um := asMap(u)
				c := pick(um)
				if c.UUID == "" {
					c.UUID = asString(um["password"])
				}
			}
		case "vmess":
			p.Enable["vmess"] = 1
			if tr := asMap(ib["transport"]); asString(tr["type"]) == "ws" && asString(tr["path"]) != "" {
				p.Paths["vmess"] = asString(tr["path"])
			}
			for _, u := range asSlice(ib["users"]) {
				pick(asMap(u))
			}
		case "vless":
			tr := asMap(ib["transport"])
			if asString(tr["type"]) == "ws" {
				p.Enable["vlws"] = 1
				p.Paths["vlws"] = firstNonEmpty(asString(tr["path"]), "/vlws")
			} else if tls := asMap(ib["tls"]); tls["reality"] != nil {
				p.Enable["reality"] = 1
				p.Ports["reality"] = port
				r := asMap(tls["reality"])
				hs := asMap(r["handshake"])
				dest := ""
				if srv := asString(hs["server"]); srv != "" {
					dest = fmt.Sprintf("%s:%d", srv, asInt(hs["server_port"]))
				}
				short := ""
				if ids := asSlice(r["short_id"]); len(ids) > 0 {
					short = asString(ids[0])
				}
				p.Reality = map[string]string{
					"server_name": asString(tls["server_name"]),
					"dest":        dest,
					"short_id":    short,
					"private_key": asString(r["private_key"]),
				}
			}
			for _, u := range asSlice(ib["users"]) {
				pick(asMap(u))
			}
		}
	}

	for _, raw := range asSlice(cfg["endpoints"]) {
		ep := asMap(raw)
		switch asString(ep["type"]) {
		case "wireguard":
			p.HasWarp = true
			addr := asString(ep["address"])
			if arr := asSlice(ep["address"]); len(arr) > 0 {
				parts := make([]string, 0, len(arr))
				for _, a := range arr {
					parts = append(parts, asString(a))
				}
				addr = strings.Join(parts, ", ")
			}
			peer := ""
			if peers := asSlice(ep["peers"]); len(peers) > 0 {
				peer = asString(asMap(peers[0])["public_key"])
			}
			p.Warp = map[string]string{
				"private_key": asString(ep["private_key"]), "address": addr, "peer_public_key": peer,
			}
		case "masque-server":
			p.Masque = true
			p.Enable["masque"] = 1
			p.MasquePort = int(asInt(ep["listen_port"]))
			p.MasquePath = asString(ep["path"])
			for _, v := range asSlice(ep["version"]) {
				n := int(asInt(v))
				if n >= 1 && n <= 3 {
					p.MasqueVersions = append(p.MasqueVersions, n)
				}
			}
		}
	}

	for _, key := range order {
		u := users[key]
		if u.UUID != "" || u.SSPassword != "" {
			p.Users = append(p.Users, *u)
		}
	}
	return p, ""
}

// POST /api/singbox/templates/from-config
func (h *handlers) singboxTemplateFromConfig(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	name := mapStr(body, "name", "")
	if name == "" {
		writeError(w, http.StatusBadRequest, "模板名称不能为空")
		return
	}
	sbText := strings.TrimSpace(mapStr(body, "sb_json", ""))
	if sbText == "" {
		writeError(w, http.StatusBadRequest, "请提供 sb.json（Xray 已下线，不再导入 xray.json）")
		return
	}
	parsed, errMsg := parseSbConfig(sbText)
	if errMsg != "" {
		writeError(w, http.StatusBadRequest, errMsg)
		return
	}
	if len(parsed.Users) == 0 {
		writeError(w, http.StatusBadRequest, "配置里没有任何用户")
		return
	}
	for _, u := range parsed.Users {
		if strings.ToLower(u.UUID) == singboxDemoUUID {
			writeError(w, http.StatusBadRequest, "配置里的客户端 UUID 还是占位符 "+singboxDemoUUID+"，请替换成真实 UUID 再保存")
			return
		}
	}

	base := 0
	for _, k := range []string{"hy2", "ss", "anytls", "reality"} {
		if v := parsed.Ports[k]; v > singboxPortDelta[k] {
			base = v - singboxPortDelta[k]
			break
		}
	}
	if base < 1 {
		base = 13700
	}
	if v := intOrJS(body["port_base"], 0, 0, 65000); v > 0 {
		base = int(v)
	}

	opts := map[string]any{}
	setOpt := func(k string, values map[string]any) {
		cur, _ := opts[k].(map[string]any)
		if cur == nil {
			cur = map[string]any{}
		}
		for kk, vv := range values {
			cur[kk] = vv
		}
		opts[k] = cur
	}
	for _, k := range []string{"ss", "anytls", "hy2", "reality"} {
		if v := parsed.Ports[k]; v > 0 {
			setOpt(k, map[string]any{"port": v})
		}
	}
	if parsed.SSMethod != "" {
		setOpt("ss", map[string]any{"method": parsed.SSMethod})
	}
	if parsed.HY2Up > 0 {
		setOpt("hy2", map[string]any{"up_mbps": parsed.HY2Up})
	}
	if parsed.HY2Down > 0 {
		setOpt("hy2", map[string]any{"down_mbps": parsed.HY2Down})
	}
	if parsed.MasquePort > 0 {
		setOpt("masque", map[string]any{"port": parsed.MasquePort})
	}
	if parsed.Paths["vmess"] != "" {
		setOpt("vmess", map[string]any{"path": parsed.Paths["vmess"]})
	}
	if parsed.Paths["vlws"] != "" {
		setOpt("vlws", map[string]any{"path": parsed.Paths["vlws"]})
	}
	if parsed.Reality["server_name"] != "" || parsed.Reality["dest"] != "" || parsed.Reality["short_id"] != "" {
		setOpt("reality", map[string]any{
			"server_name": parsed.Reality["server_name"],
			"dest":        parsed.Reality["dest"],
			"short_id":    parsed.Reality["short_id"],
		})
	}
	if parsed.MasquePath != "" {
		setOpt("masque", map[string]any{"path": parsed.MasquePath})
	}
	if len(parsed.MasqueVersions) > 0 {
		setOpt("masque", map[string]any{"versions": parsed.MasqueVersions})
	}

	optsJSON, _ := json.Marshal(opts)

	warpOn := parsed.HasWarp
	if v, ok := body["warp_enabled"]; ok {
		warpOn = singboxBool(v, false) == 1
	}
	warpKey := firstNonEmpty(mapStr(body, "warp_private_key", ""), parsed.Warp["private_key"])
	warpAddr := firstNonEmpty(mapStr(body, "warp_address", ""), parsed.Warp["address"])
	warpPeer := firstNonEmpty(mapStr(body, "warp_peer_public_key", ""), parsed.Warp["peer_public_key"])
	if warpOn && (warpKey == "" || warpAddr == "") {
		writeError(w, http.StatusBadRequest, "开启 WARP 需要填写 WARP 私钥与地址")
		return
	}

	usersJSON, _ := json.Marshal(parsed.Users)
	input := map[string]any{
		"name": name, "remark": mapStr(body, "remark", ""),
		"users_json":          string(usersJSON),
		"ss_method":           firstNonEmpty(parsed.SSMethod, singboxDefaultSSMethod),
		"ss_server_key":       parsed.SSKey,
		"reality_private_key": parsed.Reality["private_key"],
		"reality_public_key":  mapStr(body, "reality_public_key", ""),
		"reality_short_id":    parsed.Reality["short_id"],
		"reality_dest":        firstNonEmpty(parsed.Reality["dest"], "www.microsoft.com:443"),
		"reality_sni":         firstNonEmpty(parsed.Reality["server_name"], "www.microsoft.com"),
		"ws_sni":              firstNonEmpty(mapStr(body, "ws_sni", ""), "www.bing.com"),
		"cert_path":           firstNonEmpty(mapStr(body, "cert_path", ""), "/etc/sing-box/cert.crt"),
		"key_path":            firstNonEmpty(mapStr(body, "key_path", ""), "/etc/sing-box/private.key"),
		"port_base":           base,
		"warp_base":           intOrJS(body["warp_base"], 13800, 1, 65000),
		"enable_ss":           parsed.Enable["ss"], "enable_anytls": parsed.Enable["anytls"],
		"enable_hy2": parsed.Enable["hy2"], "enable_reality": parsed.Enable["reality"],
		"enable_vlws": parsed.Enable["vlws"], "enable_vmess": parsed.Enable["vmess"],
		"enable_singbox": func() int64 {
			if v, ok := body["enable_singbox"]; ok {
				return singboxBool(v, true)
			}
			if sbText != "" {
				return 1
			}
			return 0
		}(),
		"enable_masque": func() int64 {
			if parsed.Masque {
				return 1
			}
			return singboxBool(body["enable_masque"], false)
		}(),
		"masque_uri_path":  parsed.MasquePath,
		"masque_versions":  parsed.MasqueVersions,
		"warp_enabled":     singboxBool(warpOn, false),
		"warp_private_key": warpKey, "warp_address": warpAddr, "warp_peer_public_key": warpPeer,
		"protocol_options": string(optsJSON),
		"enabled":          singboxBool(body["enabled"], true),
	}

	norm, msg := normalizeSingboxTemplateInput(input)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	id := jsonInt(body["id"])
	var dup int64
	if id > 0 {
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM singbox_templates WHERE name = ? AND id <> ?`, norm["name"], id).Scan(&dup)
	} else {
		_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM singbox_templates WHERE name = ?`, norm["name"]).Scan(&dup)
	}
	if dup > 0 {
		writeError(w, http.StatusConflict, "模板名称已存在")
		return
	}
	if id > 0 {
		if err := updateDynamic(r.Context(), h.db, "singbox_templates", id, norm, nowMillis()); err != nil {
			writeError(w, http.StatusInternalServerError, "更新失败")
			return
		}
	} else {
		newID, err := insertDynamic(r.Context(), h.db, "singbox_templates", norm, nowMillis())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "创建失败")
			return
		}
		id = newID
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM singbox_templates WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusInternalServerError, "读取模板失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": singboxTemplateToJSON(rows[0], true)})
}
