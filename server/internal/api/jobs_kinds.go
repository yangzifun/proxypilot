package api

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Worker BATCH_KINDS 的剩余实现：mtproto / agent 升级 / SSH 开关 / UUID 巡检。
func init() {
	batchKinds["mtproto.apply_all"] = jobKind{
		Title: func(_ map[string]any, n int) string {
			return "一键下发全部 MTProto 节点（" + strconv.Itoa(n) + " 个）"
		},
		EmptyHint: "没有 MTProto 节点",
		Items: func(h *handlers, ctx context.Context, _ map[string]any) ([]jobItem, error) {
			rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, COALESCE(name,'') FROM mtproto_nodes ORDER BY id ASC`)
			if err != nil {
				return nil, err
			}
			out := make([]jobItem, 0, len(rows))
			for _, r := range rows {
				out = append(out, jobItem{Target: "mtproto_node", TargetID: mapInt(r, "id", 0), Label: mapStr(r, "name", "")})
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, _ map[string]any, item jobItem, _ string) (bool, string) {
			changed, queued, msg := h.applyMtprotoToNode(ctx, item.TargetID)
			if msg != "" {
				return false, msg
			}
			return true, "变更=" + strconv.FormatBool(changed) + " 排队=" + strconv.FormatBool(queued)
		},
	}

	batchKinds["mtproto.template_apply"] = jobKind{
		Title:     func(_ map[string]any, _ int) string { return "按 MTProto 模板下发" },
		EmptyHint: "模板下没有节点",
		Items: func(h *handlers, ctx context.Context, params map[string]any) ([]jobItem, error) {
			tplID := jsonInt(params["template_id"])
			if tplID <= 0 {
				return nil, nil
			}
			var name string
			if err := h.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM mtproto_templates WHERE id = ?`, tplID).Scan(&name); err != nil {
				return nil, nil
			}
			return []jobItem{{Target: "mtproto_template", TargetID: tplID, Label: name}}, nil
		},
		Run: func(h *handlers, ctx context.Context, _ map[string]any, item jobItem, actor string) (bool, string) {
			rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id FROM mtproto_nodes WHERE template_id = ? ORDER BY id ASC`, item.TargetID)
			if err != nil {
				return false, "查询节点失败"
			}
			okCount, failed := 0, 0
			for _, r := range rows {
				if _, _, msg := h.applyMtprotoToNode(ctx, mapInt(r, "id", 0)); msg != "" {
					failed++
					continue
				}
				okCount++
			}
			h.recordNodeLog(ctx, 0, 0, "batch-template", "success", 0,
				"模板 #"+strconv.FormatInt(item.TargetID, 10)+" 下发 "+strconv.Itoa(okCount)+"/"+strconv.Itoa(len(rows)), "", actor)
			return okCount > 0, "下发 " + strconv.Itoa(okCount) + "/" + strconv.Itoa(len(rows)) + " 个节点"
		},
	}

	batchKinds["agent.update"] = jobKind{
		Title: func(_ map[string]any, n int) string {
			return "一键更新 Agent（" + strconv.Itoa(n) + " 台服务器）"
		},
		EmptyHint: "没有可更新的服务器",
		Items: func(h *handlers, ctx context.Context, params map[string]any) ([]jobItem, error) {
			all, _ := params["all"].(bool)
			if all {
				rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, COALESCE(name,'') FROM servers WHERE enabled = 1 ORDER BY id ASC`)
				if err != nil {
					return nil, err
				}
				out := make([]jobItem, 0, len(rows))
				for _, r := range rows {
					out = append(out, jobItem{Target: "server", TargetID: mapInt(r, "id", 0), Label: mapStr(r, "name", "")})
				}
				return out, nil
			}
			out := []jobItem{}
			for _, v := range jsonSlice(params["server_ids"]) {
				id := jsonInt(v)
				if id <= 0 {
					continue
				}
				var name string
				_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM servers WHERE id = ?`, id).Scan(&name)
				out = append(out, jobItem{Target: "server", TargetID: id, Label: name})
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, _ map[string]any, item jobItem, actor string) (bool, string) {
			return h.queueAgentUpdateForJob(ctx, item.TargetID, actor)
		},
	}

	batchKinds["ssh.toggle"] = jobKind{
		Title: func(params map[string]any, n int) string {
			if b, _ := params["enable"].(bool); b {
				return "批量启用 SSH（" + strconv.Itoa(n) + " 台）"
			}
			return "批量停用 SSH（" + strconv.Itoa(n) + " 台）"
		},
		EmptyHint: "请选择服务器",
		Items: func(h *handlers, ctx context.Context, params map[string]any) ([]jobItem, error) {
			out := []jobItem{}
			for _, v := range jsonSlice(params["server_ids"]) {
				id := jsonInt(v)
				if id <= 0 {
					continue
				}
				var name string
				_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(name,'') FROM servers WHERE id = ?`, id).Scan(&name)
				out = append(out, jobItem{Target: "server", TargetID: id, Label: name})
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, params map[string]any, item jobItem, actor string) (bool, string) {
			enable, _ := params["enable"].(bool)
			cmd := "systemctl enable --now sshd 2>/dev/null || systemctl enable --now ssh"
			if !enable {
				cmd = "systemctl disable --now sshd 2>/dev/null || systemctl disable --now ssh; systemctl disable --now ssh.socket 2>/dev/null || true"
			}
			srv, err := h.fetchAgentServer(ctx, item.TargetID)
			if err != nil {
				return false, "服务器不存在"
			}
			taskID, dup, msg := h.agentQueueTask(ctx, srv, "restart", 0, map[string]any{"restart_cmd": cmd}, actor)
			if msg != "" {
				return false, msg
			}
			verb := "启用"
			if !enable {
				verb = "停用"
			}
			suffix := ""
			if dup {
				suffix = "，重复任务"
			}
			return true, "已排队" + verb + " SSH（任务 #" + strconv.FormatInt(taskID, 10) + suffix + "）"
		},
	}

	batchKinds["uuid.reconcile"] = jobKind{
		Title:     func(_ map[string]any, _ int) string { return "UUID 一致性巡检与修复" },
		EmptyHint: "没有需要巡检的节点",
		Items: func(h *handlers, ctx context.Context, _ map[string]any) ([]jobItem, error) {
			var n int64
			if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM singbox_nodes`).Scan(&n); err != nil || n == 0 {
				return nil, nil
			}
			return []jobItem{{Target: "all_nodes", TargetID: 0, Label: "全部 sing-box 节点"}}, nil
		},
		Run: func(h *handlers, ctx context.Context, _ map[string]any, _ jobItem, actor string) (bool, string) {
			r := h.reconcileSingboxUUIDState(ctx, actor, true)
			if r.Error != "" {
				return false, r.Error
			}
			return true, "检查 " + strconv.Itoa(r.Checked) + " 个，读取节点配置 " + strconv.Itoa(r.Probed) +
				" 个（失败 " + strconv.Itoa(r.ProbeFailed) + "），漂移 " + strconv.Itoa(r.Drifted) + "，修复 " + strconv.Itoa(r.Fixed)
		},
	}

	// singbox.apply_all 一键下发全部节点（每个节点一个任务项，后台逐台渲染 + 排队 deploy）
	batchKinds["singbox.apply_all"] = jobKind{
		Title: func(params map[string]any, n int) string {
			verb := "一键下发全部节点"
			if b, _ := params["force"].(bool); b {
				verb = "强制重下发全部节点"
			}
			return verb + "（" + strconv.Itoa(n) + " 个）"
		},
		EmptyHint: "没有 sing-box 节点",
		Items: func(h *handlers, ctx context.Context, _ map[string]any) ([]jobItem, error) {
			rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, COALESCE(name,'') FROM singbox_nodes ORDER BY id ASC`)
			if err != nil {
				return nil, err
			}
			out := make([]jobItem, 0, len(rows))
			for _, r := range rows {
				out = append(out, jobItem{Target: "singbox_node", TargetID: mapInt(r, "id", 0), Label: mapStr(r, "name", "")})
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, params map[string]any, item jobItem, _ string) (bool, string) {
			force, _ := params["force"].(bool)
			push, _ := params["push"].(bool)
			files, _, users, changed, msg := h.applySingboxNode(ctx, item.TargetID, nil, force)
			if msg != "" {
				return false, msg
			}
			queued := 0
			if push {
				for _, it := range h.queueSingboxDeploy(ctx, files, "batch-job") {
					if ok, _ := it["ok"].(bool); ok {
						queued++
					}
				}
			}
			return true, "用户=" + strconv.Itoa(len(users)) + " 变更=" + strconv.Itoa(changed) + " 排队=" + strconv.Itoa(queued)
		},
	}
}

// queueAgentUpdateForJob 与 /api/agent/push-update 共用同一套升级判断。
func (h *handlers) queueAgentUpdateForJob(ctx context.Context, serverID int64, actor string) (bool, string) {
	manifest := h.agentLatestManifest(ctx, false)
	if strings.TrimSpace(manifest.Base) == "" {
		manifest.Base = h.agentBinaryBaseURL()
	}
	if manifest.Version == "" {
		return false, firstNonEmpty(manifest.Error, "无法读取最新 Agent 版本")
	}
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, COALESCE(name,''), COALESCE(agent_platform,''), COALESCE(agent_version,'') FROM servers WHERE id = ? LIMIT 1`, serverID)
	if err != nil || len(rows) == 0 {
		return false, "服务器不存在"
	}
	row := rows[0]
	current := mapStr(row, "agent_version", "")
	if !agentVersionNewer(manifest.Version, current) {
		return true, "已是 " + firstNonEmpty(current, "最新版")
	}
	url, sha, size, ok := manifestAsset(manifest, mapStr(row, "agent_platform", ""))
	if !ok {
		return false, "未知平台 " + mapStr(row, "agent_platform", "")
	}
	if f, ok2 := manifest.Files[platformAssetKey(mapStr(row, "agent_platform", ""))].(map[string]any); ok2 {
		if _, err := os.Stat(filepath.Join(h.agentDistDir(), mapStr(f, "file", ""))); err != nil && strings.TrimSpace(h.cfg.AgentBinaryBaseURL) == "" {
			return false, "本地缺少 " + mapStr(f, "file", "")
		}
	}
	srv, err := h.fetchAgentServer(ctx, serverID)
	if err != nil {
		return false, "服务器不存在"
	}
	payload := map[string]any{"name": manifest.Version, "path": url, "sha256": sha, "size": size}
	if _, _, msg := h.agentQueueTask(ctx, srv, "update_agent", 0, payload, actor); msg != "" {
		return false, msg
	}
	return true, "已排队升级到 " + manifest.Version
}
