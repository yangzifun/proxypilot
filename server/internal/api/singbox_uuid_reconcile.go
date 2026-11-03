package api

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
)

type uuidReconcileResult struct {
	Checked      int    `json:"checked"`
	Probed       int    `json:"probed"`
	ProbeFailed  int    `json:"probe_failed"`
	ExtraConfigs int    `json:"extra_configs"`
	Orphan       int    `json:"orphan_configs"`
	Drifted      int    `json:"drifted"`
	Fixed        int    `json:"fixed"`
	Error        string `json:"error,omitempty"`
}

var uuidTextRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// reconcileSingboxUUIDState 对齐 Worker：
//  1. 节点 uuid_list（平台授权）vs 已渲染配置文件里的用户；
//  2. 每个授权 UUID 是否都有该节点生成的订阅配置；
//  3. autoFix 时按授权列表重渲染、重建 configs、排队下发。
func (h *handlers) reconcileSingboxUUIDState(ctx context.Context, actor string, autoFix bool) uuidReconcileResult {
	out := uuidReconcileResult{}
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT n.id AS id, n.server_id AS server_id,
	      COALESCE(n.name,'') AS name, COALESCE(n.uuid_list,'[]') AS uuid_list,
	      COALESCE((SELECT c.target_path FROM node_configs c WHERE c.name = CONCAT('sbx:', n.name, ':sb') ORDER BY c.id DESC LIMIT 1), '') AS target_path
	    FROM singbox_nodes n ORDER BY n.id ASC`)
	if err != nil {
		out.Error = "查询节点失败"
		return out
	}
	// 先按数据库授权去读每台节点机器上的真实配置文件（Agent 只读 inspect），再逐节点比对。
	probes := h.probeNodeConfigs(ctx, rows)
	// 全局：指向已删除节点的孤儿订阅配置
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM configs WHERE singbox_node_id > 0 AND singbox_node_id NOT IN (SELECT id FROM singbox_nodes)`).Scan(&out.Orphan)
	if out.Orphan > 0 {
		out.Drifted++
		if autoFix {
			if _, err := h.db.ExecContext(ctx,
				`DELETE FROM configs WHERE singbox_node_id > 0 AND singbox_node_id NOT IN (SELECT id FROM singbox_nodes)`); err == nil {
				out.Fixed++
			}
			h.recordNodeLog(ctx, 0, 0, "uuid-reconcile", "success", 0,
				"清理孤儿订阅配置 "+itoa64(int64(out.Orphan))+" 条（节点已删除）", "", actor)
		}
	}
	for _, row := range rows {
		nodeID := mapInt(row, "id", 0)
		nodeName := mapStr(row, "name", "")
		serverID := mapInt(row, "server_id", 0)
		var expected []string
		_ = json.Unmarshal([]byte(mapStr(row, "uuid_list", "[]")), &expected)
		out.Checked++

		onNode := map[string]bool{}
		fromMachine := false
		if p := probes[nodeID]; p != nil {
			if len(p.Users) > 0 {
				fromMachine = true
				out.Probed++
				for _, u := range p.Users {
					if s := strings.ToLower(strings.TrimSpace(u)); s != "" {
						onNode[s] = true
					}
				}
			} else if p.Err != "" {
				out.ProbeFailed++
			}
		}
		if !fromMachine {
			var content string
			_ = h.db.QueryRowContext(ctx,
				`SELECT COALESCE(content,'') FROM node_configs WHERE name = ? ORDER BY id DESC LIMIT 1`,
				"sbx:"+nodeName+":sb").Scan(&content)
			if content != "" {
				var cfg struct {
					Inbounds []struct {
						Users []struct {
							UUID     string `json:"uuid"`
							Password string `json:"password"`
						} `json:"users"`
					} `json:"inbounds"`
				}
				if json.Unmarshal([]byte(content), &cfg) == nil {
					for _, ib := range cfg.Inbounds {
						for _, u := range ib.Users {
							id := strings.TrimSpace(firstNonEmpty(u.UUID, u.Password))
							if uuidTextRe.MatchString(id) {
								onNode[strings.ToLower(id)] = true
							}
						}
					}
				}
			}
		}
		expectedLower := make(map[string]bool, len(expected))
		missingOnNode, extraOnNode, missingConfigs := 0, 0, 0
		for _, u := range expected {
			lu := strings.ToLower(strings.TrimSpace(u))
			expectedLower[lu] = true
			if !onNode[lu] {
				missingOnNode++
			}
			var c int64
			_ = h.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM configs WHERE uuid = ? AND singbox_node_id = ?`, u, nodeID).Scan(&c)
			if c == 0 {
				missingConfigs++
			}
		}
		for id := range onNode {
			if !expectedLower[id] {
				extraOnNode++
			}
		}
		// 该节点上「不该再授权」的 UUID 仍留有订阅配置（多余配置）
		extraConfigs := 0
		if crow, err := h.db.QueryContext(ctx,
			`SELECT DISTINCT uuid FROM configs WHERE singbox_node_id = ?`, nodeID); err == nil {
			for crow.Next() {
				var u string
				if crow.Scan(&u) == nil && !expectedLower[strings.ToLower(strings.TrimSpace(u))] {
					extraConfigs++
				}
			}
			_ = crow.Close()
		}
		out.ExtraConfigs += extraConfigs
		if missingOnNode == 0 && extraOnNode == 0 && missingConfigs == 0 && extraConfigs == 0 {
			continue
		}
		out.Drifted++
		source := "数据库渲染内容"
		if fromMachine {
			source = "节点机器实际配置"
		}
		h.recordNodeLog(ctx, serverID, 0, "uuid-drift", "warning", 0,
			"UUID 不一致（比对"+source+"）：缺少 "+itoa64(int64(missingOnNode))+"、多余 "+itoa64(int64(extraOnNode))+
				"、订阅配置缺 "+itoa64(int64(missingConfigs))+"、订阅配置多 "+itoa64(int64(extraConfigs)),
			"", actor)
		if !autoFix {
			continue
		}

		tpl, node, _, _, msg := h.loadSingboxNodeFull(ctx, nodeID)
		if msg != "" {
			continue
		}
		users := singboxUsersFromUUIDs(tpl, expected)
		files, _, _, _, msg := h.applySingboxNode(ctx, nodeID, users, false)
		if msg != "" {
			continue
		}
		if _, _, err := h.syncSingboxConfigsForUsers(ctx, nodeID, nodeName, tpl, node, users); err != nil {
			continue
		}
		h.queueSingboxDeploy(ctx, files, actor)
		out.Fixed++
		h.recordNodeLog(ctx, serverID, 0, "uuid-reconcile", "success", 0,
			"已按平台授权重渲染；授权 "+itoa64(int64(len(expected)))+" 个", "", actor)
	}
	result, _ := json.Marshal(map[string]any{
		"checked": out.Checked, "probed": out.Probed, "probe_failed": out.ProbeFailed,
		"extra_configs": out.ExtraConfigs, "orphan_configs": out.Orphan,
		"drifted": out.Drifted, "fixed": out.Fixed,
	})
	_ = h.putSetting(ctx, "uuid_reconcile_last_at", itoa64(nowMillis()))
	_ = h.putSetting(ctx, "uuid_reconcile_last_result", string(result))
	return out
}

// syncSingboxConfigsForUsers 按用户生成/回收该节点的订阅配置（幂等）。
func (h *handlers) syncSingboxConfigsForUsers(ctx context.Context, nodeID int64, nodeName string, tpl *singboxTemplate, node singboxRenderNode, users []sbUser) (int64, int64, error) {
	linkNode := singboxNode{Name: node.Name, Domain: node.Domain, CDNDomain: node.CDNDomain}
	desired := []map[string]any{}
	for _, u := range users {
		desired = append(desired, singboxClientLinks(tpl, linkNode, []sbUser{u})...)
	}
	uuidOf := map[string]string{}
	for _, u := range users {
		uuidOf[u.Name] = u.UUID
	}
	existing := map[string]map[string]any{}
	if rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, uuid, config_data FROM configs WHERE singbox_node_id = ?`, nodeID); err == nil {
		for _, e := range rows {
			existing[mapStr(e, "uuid", "")+"\x00"+mapStr(e, "config_data", "")] = e
		}
	}
	wanted := map[string]bool{}
	for _, l := range desired {
		uuid := uuidOf[mapStr(l, "user", "")]
		if uuid == "" {
			continue
		}
		wanted[uuid+"\x00"+mapStr(l, "link", "")] = true
	}
	removed := int64(0)
	for key, e := range existing {
		if wanted[key] {
			continue
		}
		if _, err := h.db.ExecContext(ctx, `DELETE FROM configs WHERE id = ?`, mapInt(e, "id", 0)); err == nil {
			removed++
		}
	}
	added := int64(0)
	digestParts := make([]string, 0, len(desired))
	now := nowMillis()
	for _, l := range desired {
		link := mapStr(l, "link", "")
		uuid := uuidOf[mapStr(l, "user", "")]
		if uuid == "" {
			continue
		}
		digestParts = append(digestParts, link)
		key := uuid + "\x00" + link
		if existing[key] != nil {
			continue
		}
		var dup int64
		_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM configs WHERE uuid = ? AND config_data = ?`, uuid, link).Scan(&dup)
		if dup > 0 {
			continue
		}
		remark := nodeName + "-" + mapStr(l, "protocol", "")
		if _, err := h.db.ExecContext(ctx, `INSERT INTO configs
		  (uuid, config_data, protocol, remark, domain_hosting, singbox_node_id, created_at, updated_at)
		  VALUES (?, ?, ?, ?, 'Cloudflare', ?, ?, ?)`,
			uuid, link, singboxProtocolOf(mapStr(l, "protocol", "")), truncateRunes(remark, 500), nodeID, now, now); err == nil {
			added++
		}
	}
	_, _ = h.db.ExecContext(ctx, `UPDATE singbox_nodes SET last_link_hash = ?, updated_at = ? WHERE id = ?`,
		sha256Hex(strings.Join(digestParts, "\n")), now, nodeID)
	return added, removed, nil
}
