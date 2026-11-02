package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

func (h *handlers) ensureSingboxNodeForServer(ctx context.Context, serverID int64) (int64, string, string) {
	var nodeID int64
	err := h.db.QueryRowContext(ctx, `SELECT id FROM singbox_nodes WHERE server_id = ? LIMIT 1`, serverID).Scan(&nodeID)
	if err == nil {
		return nodeID, "", ""
	}
	if err != sql.ErrNoRows {
		return 0, "", "查询节点失败"
	}
	var (
		serverName, host, domain string
		templateID               int64
		warp                     int64 = -1
	)
	err = h.db.QueryRowContext(ctx, `SELECT COALESCE(name,''), COALESCE(host,''), COALESCE(singbox_domain,''),
	      COALESCE(singbox_template_id,0), COALESCE(singbox_warp,-1) FROM servers WHERE id = ? LIMIT 1`, serverID).
		Scan(&serverName, &host, &domain, &templateID, &warp)
	if err == sql.ErrNoRows {
		return 0, "", "服务器不存在"
	}
	if err != nil {
		return 0, "", "查询服务器失败"
	}
	if templateID <= 0 {
		_ = h.db.QueryRowContext(ctx, `SELECT id FROM singbox_templates WHERE enabled = 1 ORDER BY id ASC LIMIT 1`).Scan(&templateID)
	}
	if templateID <= 0 {
		return 0, "", "没有可用的协议模板"
	}
	base := truncateRunes(serverName, 44)
	if base == "" {
		base = fmt.Sprintf("node%d", serverID)
	}
	name := base
	for i := 2; i < 50; i++ {
		var dup int64
		_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM singbox_nodes WHERE name = ?`, name).Scan(&dup)
		if dup == 0 {
			break
		}
		name = fmt.Sprintf("%s-%d", truncateRunes(base, 44), i)
	}
	dom := firstNonEmpty(strings.TrimSpace(domain), strings.TrimSpace(host))
	res, err := h.db.ExecContext(ctx, `INSERT INTO singbox_nodes
	  (server_id, template_id, name, domain, warp_enabled, enabled, remark, created_at, updated_at)
	  VALUES (?, ?, ?, ?, ?, 1, 'UUID 授权推送自动创建', ?, ?)`,
		serverID, templateID, name, dom, warp, nowMillis(), nowMillis())
	if err != nil {
		return 0, "", "创建节点失败：" + err.Error()
	}
	id, _ := res.LastInsertId()
	return id, name, ""
}

// POST /api/singbox/push —— UUID × 服务器批量授权
func (h *handlers) singboxPush(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	uuids := []string{}
	for _, v := range jsonSlice(body["uuids"]) {
		s := strings.TrimSpace(jsonStr(v))
		if s != "" && len(uuids) < 200 {
			uuids = append(uuids, s)
		}
	}
	serverIDs := []int64{}
	for _, v := range jsonSlice(body["server_ids"]) {
		id := jsonInt(v)
		if id > 0 && len(serverIDs) < 100 {
			serverIDs = append(serverIDs, id)
		}
	}
	if len(uuids) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一个 UUID")
		return
	}
	if len(serverIDs) == 0 {
		writeError(w, http.StatusBadRequest, "请至少选择一台服务器")
		return
	}
	doPush := true
	if v, ok := body["push"].(bool); ok {
		doPush = v
	}

	out := make([]map[string]any, 0, len(serverIDs))
	for _, sid := range serverIDs {
		nodeID, _, msg := h.ensureSingboxNodeForServer(r.Context(), sid)
		if msg != "" {
			out = append(out, map[string]any{"server_id": sid, "ok": false, "error": msg})
			continue
		}
		tpl, _, _, nodeRow, msg := h.loadSingboxNodeFull(r.Context(), nodeID)
		if msg != "" {
			out = append(out, map[string]any{"server_id": sid, "ok": false, "error": msg})
			continue
		}
		users := singboxUsersFromUUIDs(tpl, uuids)
		files, _, outUsers, changed, msg := h.applySingboxNode(r.Context(), nodeID, users, false)
		if msg != "" {
			out = append(out, map[string]any{"server_id": sid, "name": mapStr(nodeRow, "name", ""), "ok": false, "error": msg})
			continue
		}
		uuidJSON, _ := json.Marshal(uuids)
		_, _ = h.db.ExecContext(r.Context(),
			`UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`, string(uuidJSON), nowMillis(), nodeID)
		queued := 0
		if doPush {
			for _, item := range h.queueSingboxDeploy(r.Context(), files, "push") {
				if ok, _ := item["ok"].(bool); ok {
					queued++
				}
			}
		}
		out = append(out, map[string]any{
			"server_id": sid, "name": mapStr(nodeRow, "name", ""), "ok": true,
			"users": len(outUsers), "files": len(files), "changed": changed, "queued": queued,
		})
	}
	okCount := 0
	for _, x := range out {
		if ok, _ := x["ok"].(bool); ok {
			okCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": okCount > 0, "data": out, "ok_count": okCount, "total": len(out)})
}

// ---------------- releases（GitHub，6 小时内存缓存） ----------------

type releaseItem struct {
	Tag         string `json:"tag"`
	Name        string `json:"name"`
	Prerelease  bool   `json:"prerelease"`
	PublishedAt string `json:"published_at"`
}

type releaseCacheEntry struct {
	At   time.Time
	List []releaseItem
}

var (
	releaseMu    sync.Mutex
	releaseCache = map[string]releaseCacheEntry{}
)

func (h *handlers) singboxReleases(w http.ResponseWriter, r *http.Request) {
	// V15.6：Xray 已下线，只提供 sing-box 的版本列表。
	if strings.ToLower(strings.TrimSpace(r.URL.Query().Get("engine"))) == "xray" {
		writeError(w, http.StatusBadRequest, "Xray 已下线，仅在节点管理里使用 sing-box")
		return
	}
	engine := "singbox"
	releaseMu.Lock()
	if e, ok := releaseCache[engine]; ok && time.Since(e.At) < 6*time.Hour {
		releaseMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "engine": engine, "cached": true, "data": e.List})
		return
	}
	releaseMu.Unlock()

	repo := "SagerNet/sing-box"
	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases?per_page=20", nil)
	req.Header.Set("User-Agent", "ProxyPilot")
	req.Header.Set("Accept", "application/vnd.github+json")

	list := []releaseItem{}
	warning := ""
	resp, err := client.Do(req)
	if err != nil {
		warning = "拉取 GitHub Release 失败：" + err.Error()
	} else {
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			warning = fmt.Sprintf("拉取 GitHub Release 失败：GitHub HTTP %d", resp.StatusCode)
		} else {
			var arr []struct {
				TagName     string `json:"tag_name"`
				Name        string `json:"name"`
				Draft       bool   `json:"draft"`
				Prerelease  bool   `json:"prerelease"`
				PublishedAt string `json:"published_at"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
				warning = "解析 GitHub Release 失败：" + err.Error()
			} else {
				for _, rel := range arr {
					if rel.Draft {
						continue
					}
					name := rel.Name
					if name == "" {
						name = rel.TagName
					}
					list = append(list, releaseItem{Tag: rel.TagName, Name: name, Prerelease: rel.Prerelease, PublishedAt: rel.PublishedAt})
				}
				releaseMu.Lock()
				if len(releaseCache) > 8 {
					releaseCache = map[string]releaseCacheEntry{}
				}
				releaseCache[engine] = releaseCacheEntry{At: time.Now(), List: list}
				releaseMu.Unlock()
			}
		}
	}
	if len(list) == 0 {
		releaseMu.Lock()
		cached := releaseCache[engine].List
		releaseMu.Unlock()
		if len(cached) > 0 {
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "engine": engine, "cached": true, "data": cached, "warning": warning})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"success": false, "engine": engine, "cached": false, "data": []any{}, "warning": warning})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "engine": engine, "cached": false, "data": list, "warning": warning})
}
