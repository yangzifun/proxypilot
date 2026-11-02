package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
)

type unlockServiceDef struct {
	Key   string
	Name  string
	Group string
	URL   string
}

var unlockServices = []unlockServiceDef{
	{"chatgpt", "ChatGPT / OpenAI", "ai", "https://chatgpt.com/cdn-cgi/trace"},
	{"claude", "Claude", "ai", "https://claude.ai/"},
	{"gemini", "Gemini", "ai", "https://gemini.google.com/app"},
	{"copilot", "Microsoft Copilot", "ai", "https://copilot.microsoft.com/"},
	{"netflix", "Netflix", "stream", "https://www.netflix.com/title/81215567"},
	{"youtube", "YouTube Premium", "stream", "https://www.youtube.com/premium"},
	{"disney", "Disney+", "stream", "https://www.disneyplus.com/"},
	{"primevideo", "Prime Video", "stream", "https://www.primevideo.com/"},
	{"spotify", "Spotify", "stream", "https://open.spotify.com/"},
	{"tiktok", "TikTok", "stream", "https://www.tiktok.com/"},
}

// GET/POST /api/servers/{id}/unlock —— 查询缓存 / 发起解锁检测。
func (h *handlers) serverUnlock(w http.ResponseWriter, r *http.Request) {
	id, ok := serverID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}
	var exists int
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM servers WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		writeError(w, http.StatusNotFound, "服务器不存在")
		return
	}

	if r.Method == http.MethodPost {
		srv, err := h.fetchAgentServer(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusNotFound, "服务器不存在")
			return
		}
		taskID, dup, msg := h.agentQueueTask(r.Context(), srv, "unlock", 0,
			map[string]any{"timeout": 300}, "admin")
		if msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
		_, _ = h.db.ExecContext(r.Context(), `INSERT INTO server_unlock_checks
		  (server_id, results, checked_at, task_id, updated_at) VALUES (?, '{}', 0, ?, ?)
		  ON DUPLICATE KEY UPDATE task_id = VALUES(task_id), updated_at = VALUES(updated_at)`,
			id, taskID, nowMillis())
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "queued": true, "task_id": taskID, "duplicate": dup,
			"message": "解锁检测已下发，通常需要 30–90 秒",
		})
		return
	}

	if taskID := intOrJS(r.URL.Query().Get("task_id"), 0, 0, 1<<62); taskID > 0 {
		var status, output string
		err := h.db.QueryRowContext(r.Context(),
			`SELECT COALESCE(status,''), COALESCE(output,'') FROM agent_tasks WHERE id = ? AND server_id = ?`,
			taskID, id).Scan(&status, &output)
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "检测任务不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "读取检测任务失败")
			return
		}
		switch status {
		case "done":
			data := parseUnlockOutput(output)
			if len(data.Services) == 0 {
				writeJSON(w, http.StatusOK, map[string]any{
					"success": true, "status": "failed",
					"error": "检测未返回有效结果，请确认服务器已安装 curl",
				})
				return
			}
			data.CheckedAt = nowMillis()
			raw, _ := json.Marshal(data)
			_, _ = h.db.ExecContext(r.Context(), `INSERT INTO server_unlock_checks
			  (server_id, results, checked_at, task_id, updated_at) VALUES (?, ?, ?, 0, ?)
			  ON DUPLICATE KEY UPDATE results = VALUES(results), checked_at = VALUES(checked_at), task_id = 0, updated_at = VALUES(updated_at)`,
				id, string(raw), data.CheckedAt, data.CheckedAt)
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "status": "done", "data": data})
		case "failed", "expired":
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true, "status": "failed",
				"error": firstNonEmpty(strings.TrimSpace(output), "检测任务执行失败"),
			})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "status": "pending", "task_id": taskID})
		}
		return
	}

	var raw string
	err := h.db.QueryRowContext(r.Context(), `SELECT results FROM server_unlock_checks WHERE server_id = ?`, id).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取解锁结果失败")
		return
	}
	var data unlockResult
	if json.Unmarshal([]byte(raw), &data) != nil || len(data.Services) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data})
}

type unlockResult struct {
	CheckedAt int64           `json:"checked_at"`
	Services  []unlockService `json:"services"`
}

type unlockService struct {
	Key    string `json:"key"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	Status string `json:"status"`
	Code   string `json:"code"`
	URL    string `json:"url"`
	Region string `json:"region"`
	Detail string `json:"detail"`
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func parseUnlockOutput(output string) unlockResult {
	byKey := map[string]unlockService{}
	for _, def := range unlockServices {
		byKey[def.Key] = unlockService{
			Key: def.Key, Name: def.Name, Group: def.Group, Status: "unknown", Detail: "未返回结果",
		}
	}
	for _, line := range strings.Split(stripANSI(output), "\n") {
		line = strings.TrimSpace(line)
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key, val := strings.TrimSpace(line[:eq]), strings.TrimSpace(line[eq+1:])
		def, ok := unlockDef(key)
		if !ok {
			continue
		}
		code, effective, region := val, "", ""
		if parts := strings.SplitN(val, "|", 3); len(parts) > 0 {
			code = parts[0]
			if len(parts) > 1 {
				effective = parts[1]
			}
			if len(parts) > 2 {
				region = parts[2]
			}
		}
		byKey[key] = unlockService{
			Key: def.Key, Name: def.Name, Group: def.Group, Code: code,
			Status: classifyHTTP(code, effective), URL: effective, Region: region,
			Detail: httpDetail(code, effective, region),
		}
	}
	applyCMT(byKey, output)
	applyRRC(byKey, output)
	// 用户约定：没有更精确结论时，HTTP 200 可达即视为解锁。
	for key, s := range byKey {
		if s.Status == "uncertain" && s.Code == "200" {
			s.Status = "unlocked"
			s.Detail = "HTTP 200 可达，视为解锁"
			if s.Region != "" {
				s.Detail += "；出口地区 " + s.Region
			}
			byKey[key] = s
		}
	}
	out := unlockResult{Services: make([]unlockService, 0, len(unlockServices))}
	for _, def := range unlockServices {
		out.Services = append(out.Services, byKey[def.Key])
	}
	return out
}

// applyCMT 解析 CommonMediaTests 的真实流媒体解锁结果（Netflix / Disney+）。
func applyCMT(byKey map[string]unlockService, output string) {
	clean := stripANSI(output)
	netflix := between(clean, "----------------Netflix", "----------------Youtube")
	if netflix != "" {
		s := byKey["netflix"]
		s.Code = "200"
		region := ""
		for _, line := range strings.Split(netflix, "\n") {
			if strings.Contains(line, "地域信息") {
				region = strings.TrimSpace(line)
			}
		}
		switch {
		case strings.Contains(netflix, "完整解锁Netflix"):
			s.Status, s.Detail = "unlocked", "完整解锁，支持非自制剧"
		case strings.Contains(netflix, "仅自制剧"):
			s.Status, s.Detail = "partial", "仅解锁自制剧"
		case strings.Contains(netflix, "未解锁") || strings.Contains(netflix, "无法观看") ||
			strings.Contains(netflix, "无法正常使用") || strings.Contains(netflix, "疑似代理") ||
			strings.Contains(netflix, "不提供服务"):
			s.Status, s.Detail = "blocked", "IP 疑似代理或地区限制，无法正常使用"
		default:
			s.Status, s.Detail = "uncertain", "CommonMediaTests 未给出明确结论"
		}
		if region != "" {
			s.Detail += "；" + region
		}
		byKey["netflix"] = s
	}
	disney := between(clean, "---------------DisneyPlus", "")
	if disney != "" {
		s := byKey["disney"]
		s.Code = "200"
		switch {
		case strings.Contains(disney, "已开通") || strings.Contains(disney, "完整解锁"):
			s.Status, s.Detail = "unlocked", "当前地区已开通 Disney+"
		case strings.Contains(disney, "即将开通"):
			s.Status, s.Detail = "blocked", "当前地区尚未开通 Disney+"
		case strings.Contains(disney, "未开通") || strings.Contains(disney, "不支持") ||
			strings.Contains(disney, "不提供服务"):
			s.Status, s.Detail = "blocked", "当前地区未开通或不提供 Disney+"
		default:
			s.Status, s.Detail = "uncertain", "CommonMediaTests 未给出明确结论"
		}
		byKey["disney"] = s
	}
}

// applyRRC 解析 RegionRestrictionCheck 的实测结果，覆盖 AI / YouTube / Prime / Spotify 的可达性。
func applyRRC(byKey map[string]unlockService, output string) {
	labels := []struct{ label, key string }{
		{"YouTube Premium", "youtube"},
		{"Amazon Prime Video", "primevideo"},
		{"Spotify Registration", "spotify"},
		{"ChatGPT", "chatgpt"},
		{"Claude", "claude"},
	}
	for _, line := range strings.Split(stripANSI(output), "\n") {
		line = strings.TrimSpace(line)
		for _, item := range labels {
			idx := strings.Index(line, item.label+":")
			if idx < 0 {
				continue
			}
			val := strings.TrimSpace(line[idx+len(item.label)+1:])
			if val == "" {
				continue
			}
			s := byKey[item.key]
			s.Code = ""
			lower := strings.ToLower(val)
			switch {
			case strings.Contains(lower, "yes") || strings.Contains(val, "是"):
				if item.key == "spotify" {
					s.Status, s.Detail = "available", "可注册："+val
				} else {
					s.Status, s.Detail = "unlocked", "实测可用："+val
				}
			case strings.Contains(lower, "only available"), strings.Contains(val, "仅"):
				s.Status, s.Detail = "partial", val
			case strings.Contains(lower, "no"), strings.Contains(lower, "failed"), strings.Contains(val, "不可"):
				s.Status, s.Detail = "blocked", "实测不可用："+val
			default:
				s.Status, s.Detail = "uncertain", val
			}
			if m := rrcRegionRE.FindStringSubmatch(val); len(m) > 1 {
				s.Region = strings.TrimSpace(m[1])
			}
			byKey[item.key] = s
			break
		}
	}
}

var rrcRegionRE = regexp.MustCompile(`\(Region:\s*([^)]*)\)`)

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	if end != "" {
		if j := strings.Index(s, end); j >= 0 {
			return s[:j]
		}
	}
	return s
}

func unlockDef(key string) (unlockServiceDef, bool) {
	for _, d := range unlockServices {
		if d.Key == key {
			return d, true
		}
	}
	return unlockServiceDef{}, false
}

func classifyHTTP(code, effective string) string {
	code = strings.TrimSpace(code)
	lower := strings.ToLower(effective)
	for _, marker := range []string{"unavailable-in-region", "unsupportedbrowser", "/unavailable", "not-available"} {
		if strings.Contains(lower, marker) {
			return "blocked"
		}
	}
	if code == "" || code == "000" {
		return "unknown"
	}
	switch {
	case strings.HasPrefix(code, "2"), strings.HasPrefix(code, "3"):
		return "uncertain"
	case code == "401" || code == "403" || code == "451":
		return "blocked"
	default:
		return "warn"
	}
}

func httpDetail(code, effective, region string) string {
	code = strings.TrimSpace(code)
	lower := strings.ToLower(effective)
	if strings.Contains(lower, "unavailable-in-region") {
		return "当前地区不可用"
	}
	if strings.Contains(lower, "unsupportedbrowser") {
		return "浏览器不受支持或地区限制"
	}
	if strings.Contains(lower, "/unavailable") || strings.Contains(lower, "not-available") {
		return "当前地区不可用"
	}
	if code == "" || code == "000" {
		return "连接超时或被拒绝"
	}
	if strings.HasPrefix(code, "2") || strings.HasPrefix(code, "3") {
		if region != "" {
			return "HTTP 可达，尚不能确认账号可用性；出口地区 " + region
		}
		return "HTTP 可达，尚不能确认账号可用性"
	}
	if code == "401" || code == "403" || code == "451" {
		return "服务拒绝访问（地区或风控限制）"
	}
	return "HTTP " + code
}
