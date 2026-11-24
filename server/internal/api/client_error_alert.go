package api

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"
	"sync"
	"time"
)

// clientErrorAlertSendMail 可测试替换；生产默认走现有 h.sendMail。
var clientErrorAlertSendMail = func(h *handlers, to, subject, body string) error {
	return h.sendMail(to, subject, body)
}

var clientErrorAlertMu sync.Mutex

// triggerClientErrorAlertAsync 在 POST 落库后异步检查，不阻塞上报请求。
func (h *handlers) triggerClientErrorAlertAsync() {
	if h.db == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil && h.log != nil {
				h.log.Warn("客户端错误告警异常（已恢复）", "panic", fmt.Sprint(r))
			}
		}()
		if !clientErrorAlertMu.TryLock() {
			return
		}
		defer clientErrorAlertMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := h.maybeAlertClientErrors(ctx, nowMillis()); err != nil && h.log != nil {
			h.log.Warn("客户端错误告警发送失败", "err", err)
		}
	}()
}

// maybeAlertClientErrors 检查最近 5 分钟错误量并发送告警邮件。
// 返回 nil 表示未触发/已静默跳过/发送成功；发送失败返回错误供异步日志记录。
func (h *handlers) maybeAlertClientErrors(ctx context.Context, now int64) error {
	if h.db == nil {
		return nil
	}
	if !h.settingBool(ctx, "client_error_alert_enabled", false) {
		return nil
	}
	threshold := int64(intOrJS(h.getSetting(ctx, "client_error_alert_threshold", "20"), 20, 1, 1000))
	cooldown := int64(intOrJS(h.getSetting(ctx, "client_error_alert_cooldown_min", "30"), 30, 5, 1440))
	last := int64(intOrJS(h.getSetting(ctx, "client_error_alert_at", "0"), 0, 0, 1<<60))
	if now-last < cooldown*60_000 {
		return nil
	}

	to := normalizeEmailList(h.getSetting(ctx, "client_error_alert_emails", ""))
	if to == "" {
		to = normalizeEmailList(h.getSetting(ctx, "monitor_notify_emails", ""))
	}
	if to == "" {
		return nil
	}

	since := now - 5*60*1000
	var count int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM client_errors WHERE created_at >= ?`, since).Scan(&count); err != nil {
		return err
	}
	if count < threshold {
		return nil
	}

	type modeCount struct {
		Mode  string
		Count int64
	}
	modes := []modeCount{}
	rows, err := h.db.QueryContext(ctx,
		`SELECT COALESCE(mode,''), COUNT(*) FROM client_errors
		  WHERE created_at >= ? GROUP BY mode ORDER BY COUNT(*) DESC`, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m string
		var c int64
		if rows.Scan(&m, &c) == nil {
			modes = append(modes, modeCount{Mode: m, Count: c})
		}
	}
	rows.Close()

	type messageCount struct {
		Message string
		Count   int64
	}
	messages := []messageCount{}
	rows, err = h.db.QueryContext(ctx,
		`SELECT COALESCE(message,''), COUNT(*) FROM client_errors
		  WHERE created_at >= ? AND COALESCE(message,'') <> ''
		  GROUP BY message ORDER BY COUNT(*) DESC LIMIT 5`, since)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m string
		var c int64
		if rows.Scan(&m, &c) == nil {
			messages = append(messages, messageCount{Message: truncateRunes(m, 120), Count: c})
		}
	}
	rows.Close()

	var intro strings.Builder
	fmt.Fprintf(&intro, "检测到前端错误持续增加。<br>最近 5 分钟：<b>%d</b> 条<br>时间：%s",
		count, time.UnixMilli(now).Format("2006-01-02 15:04:05 MST"))
	if len(modes) > 0 {
		intro.WriteString("<br>按来源：<ul>")
		for _, m := range modes {
			fmt.Fprintf(&intro, "<li>%s：%d</li>", html.EscapeString(m.Mode), m.Count)
		}
		intro.WriteString("</ul>")
	}
	if len(messages) > 0 {
		intro.WriteString("<br>Top 消息：<ul>")
		for _, m := range messages {
			fmt.Fprintf(&intro, "<li>%s（%d 次）</li>", html.EscapeString(m.Message), m.Count)
		}
		intro.WriteString("</ul>")
	}
	if base := h.publicBaseURL(ctx); base != "" {
		fmt.Fprintf(&intro, `<br>面板地址：<a href="%s">%s</a>`, html.EscapeString(base), html.EscapeString(base))
	}
	body := emailHTML("客户端错误告警", intro.String(), "", 0)
	subject := "【代理配置管理】前端错误告警"

	sent := false
	var lastErr error
	for _, addr := range strings.Split(to, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if err := clientErrorAlertSendMail(h, addr, subject, body); err != nil {
			if errors.Is(err, errMailNotConfigured) {
				return nil // 邮件未配置：静默跳过
			}
			lastErr = err
			if h.log != nil {
				h.log.Warn("客户端错误告警邮件发送失败", "to", maskEmailForLog(addr), "err", err)
			}
			continue
		}
		sent = true
	}
	if sent {
		_ = h.putSetting(ctx, "client_error_alert_at", itoa64(now))
		h.invalidateSettings()
		return nil
	}
	return lastErr
}
