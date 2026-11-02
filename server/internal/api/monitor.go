package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

// GET/POST /api/monitor/notify —— 与 Worker 一致：邮件通知（enabled / emails / cooldown_min）
func (h *handlers) monitorNotify(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var body map[string]any
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
		enabled := "0"
		if singboxBool(body["enabled"], false) == 1 {
			enabled = "1"
		}
		emails := normalizeEmailList(mapStr(body, "emails", ""))
		cooldown := intOrJS(body["cooldown_min"], 10, 1, 1440)
		if enabled == "1" && emails == "" {
			writeError(w, http.StatusBadRequest, "开启通知至少要填一个收件邮箱")
			return
		}
		_ = h.putSetting(ctx, "monitor_notify_enabled", enabled)
		_ = h.putSetting(ctx, "monitor_notify_emails", emails)
		_ = h.putSetting(ctx, "monitor_notify_cooldown_min", itoa64(cooldown))
	}
	mail := h.effectiveMail(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"email_ready":  mail.Addr != "" && mail.From != "",
		"enabled":      h.getSetting(ctx, "monitor_notify_enabled", "0") == "1",
		"emails":       h.getSetting(ctx, "monitor_notify_emails", ""),
		"cooldown_min": intOrJS(h.getSetting(ctx, "monitor_notify_cooldown_min", "10"), 10, 1, 1440),
	})
}

func normalizeEmailList(raw string) string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ';' || r == '\n' || r == ' ' })
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		e := normalizeEmail(p)
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return strings.Join(out, ",")
}

// POST /api/monitor/notify/test
func (h *handlers) monitorNotifyTest(w http.ResponseWriter, r *http.Request) {
	emails := normalizeEmailList(h.getSetting(r.Context(), "monitor_notify_emails", ""))
	if emails == "" {
		writeError(w, http.StatusBadRequest, "还没有配置收件邮箱")
		return
	}
	list := strings.Split(emails, ",")
	intro := fmt.Sprintf(`通知通道已配置成功。<br>时间：%s<br>版本：%s`,
		time.Now().Format("2006-01-02 15:04:05 MST"), Version)
	if base := h.publicBaseURL(r.Context()); base != "" {
		intro += fmt.Sprintf(`<br>后台地址：<a href="%s">%s</a>`, html.EscapeString(base), html.EscapeString(base))
	}
	body := emailHTML("ProxyPilot 测试通知", intro, "", 0)
	for _, to := range list {
		if err := h.sendMail(to, "【代理配置管理】测试通知", body); err != nil {
			writeError(w, http.StatusBadGateway, "发送失败："+err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "to": list})
}

// GET/POST /api/monitor/uuid-check
func (h *handlers) monitorUUIDCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var body map[string]any
	if r.Method == http.MethodPost {
		if err := decodeJSON(w, r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "无效 JSON")
			return
		}
	} else if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}

	if r.Method == http.MethodGet {
		interval := intOrJS(h.getSetting(ctx, "uuid_reconcile_interval_min", "30"), 30, 5, 1440)
		lastAt := intOrJS(h.getSetting(ctx, "uuid_reconcile_last_at", "0"), 0, 0, 1<<60)
		var result any = map[string]any{}
		_ = json.Unmarshal([]byte(h.getSetting(ctx, "uuid_reconcile_last_result", "{}")), &result)
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "last_at": lastAt, "last_result": result, "interval_min": interval,
		})
		return
	}

	rr := h.reconcileSingboxUUIDState(ctx, "admin", true)
	if v := strings.TrimSpace(mapStr(body, "interval_min", "")); v != "" {
		_ = h.putSetting(ctx, "uuid_reconcile_interval_min", itoa64(intOrJS(v, 30, 5, 1440)))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success": rr.Error == "", "checked": rr.Checked, "drifted": rr.Drifted,
		"fixed": rr.Fixed, "problems": rr.Drifted, "at": nowMillis(), "error": rr.Error,
	})
}
