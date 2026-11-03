package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func manifestAsset(manifest manifestData, platform string) (url, sha string, size int64, ok bool) {
	p := strings.ToLower(strings.TrimSpace(platform))
	key := ""
	switch p {
	case "linux/arm64":
		key = "linux-arm64"
	case "linux/amd64", "":
		key = "linux-amd64"
	default:
		return "", "", 0, false
	}
	f, exists := manifest.Files[key]
	if !exists {
		return "", "", 0, false
	}
	m, _ := f.(map[string]any)
	file := mapStr(m, "file", "")
	if file == "" {
		return "", "", 0, false
	}
	base := strings.TrimRight(manifest.Base, "/")
	sha = mapStr(m, "sha256", "")
	u := base + "/" + file
	if sha != "" { // 同名文件每版覆盖：带 sha 参数绕过 CDN 缓存
		u += "?sha256=" + sha
	}
	return u, sha, mapInt(m, "size", 0), true
}

// POST /api/agent/push-update —— 给 Agent 下发升级任务
func (h *handlers) agentPushUpdate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = decodeJSON(w, r, &body)
	if body == nil {
		body = map[string]any{}
	}
	manifest := h.agentLatestManifest(r.Context(), r.URL.Query().Get("refresh") == "1")
	if strings.TrimSpace(manifest.Base) == "" {
		manifest.Base = h.agentBaseFor(r)
	}
	if manifest.Version == "" {
		writeError(w, http.StatusBadRequest, firstNonEmpty(manifest.Error, "无法读取最新 agent 版本（manifest.json）"))
		return
	}

	all, _ := body["all"].(bool)
	var ids []int64
	for _, v := range jsonSlice(body["server_ids"]) {
		if id := jsonInt(v); id > 0 && len(ids) < 200 {
			ids = append(ids, id)
		}
	}
	targets := []map[string]any{}
	if all {
		rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT id, name, COALESCE(agent_platform,''), COALESCE(agent_version,'')
		    FROM servers ORDER BY id ASC`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "查询服务器失败")
			return
		}
		targets = rows
	} else {
		if len(ids) == 0 {
			writeError(w, http.StatusBadRequest, "请选择要升级的服务器")
			return
		}
		for _, id := range ids {
			rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT id, name, COALESCE(agent_platform,''), COALESCE(agent_version,'')
			    FROM servers WHERE id = ? LIMIT 1`, id)
			if err == nil && len(rows) > 0 {
				targets = append(targets, rows[0])
			}
		}
	}

	// 多台服务器时转后台任务（逐台排队 update_agent，避免请求线程里逐台处理）
	if len(targets) > 1 {
		params := map[string]any{"all": all}
		if !all {
			idsAny := make([]any, 0, len(ids))
			for _, id := range ids {
				idsAny = append(idsAny, id)
			}
			params["server_ids"] = idsAny
		}
		jobID, n, dup, msg := h.batchCreateJob(r.Context(), "agent.update", params, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		if !dup {
			h.startBatchJob(jobID, "admin")
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "async": true, "job_id": jobID, "total": n, "duplicate": dup,
			"latest": manifest.Version, "message": "已创建后台任务，进度见右上角「任务」",
		})
		return
	}

	out := make([]map[string]any, 0, len(targets))
	for _, srv := range targets {
		id := mapInt(srv, "id", 0)
		name := mapStr(srv, "name", "")
		current := mapStr(srv, "agent_version", "")
		if !agentVersionNewer(manifest.Version, current) {
			out = append(out, map[string]any{
				"server_id": id, "name": name, "ok": true, "skipped": true,
				"reason": "已是 " + firstNonEmpty(current, "未知版本"),
			})
			continue
		}
		url, sha, size, ok := manifestAsset(manifest, mapStr(srv, "agent_platform", ""))
		if ok && strings.TrimSpace(h.cfg.AgentBinaryBaseURL) == "" {
			// 本地分发：确认二进制确实存在，避免排队一个必然 404 的任务
			if f, ok2 := manifest.Files[platformAssetKey(mapStr(srv, "agent_platform", ""))].(map[string]any); ok2 {
				if _, err := os.Stat(filepath.Join(h.agentDistDir(), mapStr(f, "file", ""))); err != nil {
					out = append(out, map[string]any{
						"server_id": id, "name": name, "ok": false,
						"error": "本地缺少 " + mapStr(f, "file", "") + "，请先执行 agent/build.sh 并放入 agent/dist",
					})
					continue
				}
			}
		}
		if !ok {
			out = append(out, map[string]any{
				"server_id": id, "name": name, "ok": false,
				"error": "未知平台 " + mapStr(srv, "agent_platform", ""),
			})
			continue
		}
		agent, err := h.fetchAgentServer(r.Context(), id)
		if err != nil {
			out = append(out, map[string]any{"server_id": id, "name": name, "ok": false, "error": "服务器不存在"})
			continue
		}
		payload := map[string]any{"name": manifest.Version, "path": url, "sha256": sha, "size": size}
		taskID, dup, msg := h.agentQueueTask(r.Context(), agent, "update_agent", 0, payload, "update")
		out = append(out, map[string]any{
			"server_id": id, "name": name, "ok": msg == "", "task_id": taskID,
			"duplicate": dup, "error": msg, "latest": manifest.Version,
		})
	}
	queued := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			if skipped, _ := x["skipped"].(bool); !skipped {
				queued++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "latest": manifest.Version, "results": out, "queued": queued,
	})
}
