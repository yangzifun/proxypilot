package api

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// StartCronFor 提供进程内定时任务，替代 Worker 的 Cron Trigger：
//   - Agent 心跳判定（按 poll 间隔自适应）+ 离线邮件通知
//   - 任务回收 / 过期 / 清理
//   - 批量任务队列恢复、保留期清理
//   - UUID 一致性巡检（按 uuid_reconcile_interval_min）
//   - 指标保留
func StartCronFor(ctx context.Context, cfg config.Config, db *sql.DB, log *slog.Logger) {
	if db == nil {
		return
	}
	h := &handlers{cfg: cfg, db: db, log: log}
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		h.runCronOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.runCronOnce(ctx)
			}
		}
	}()
}

// StartCron 兼容签名（无 cfg，后台设置仍从 DB 读取）。
func StartCron(ctx context.Context, db *sql.DB, log *slog.Logger) {
	StartCronFor(ctx, config.Config{}, db, log)
}

func (h *handlers) runCronOnce(ctx context.Context) {
	now := time.Now().UnixMilli()
	h.geoRefreshAllThrottled(ctx)
	h.ensureMihomoDefaultTemplate(ctx)
	h.cronOffline(ctx, now)
	h.cronTaskMaintenance(ctx, now)
	h.cronResumeJobs(ctx, now)
	h.cronCleanup(ctx, now)
	h.cronUUIDReconcile(ctx, now)
	h.cronCDNPreferred(ctx, now)
	h.cronTrafficReset(ctx, now)
}

func (h *handlers) cronOffline(ctx context.Context, now int64) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id,
		        COALESCE(name,'') AS name,
		        COALESCE(agent_next_poll_sec,2) AS agent_next_poll_sec,
		        COALESCE(last_check_at,0) AS last_check_at,
		        COALESCE(agent_last_seen,0) AS agent_last_seen,
		        COALESCE(last_status,'') AS last_status
		   FROM servers WHERE enabled = 1 AND COALESCE(last_status,'') <> 'offline'`)
	if err != nil {
		return
	}
	offline := []string{}
	notices := []serverStatusNotice{}
	for _, r := range rows {
		id := mapInt(r, "id", 0)
		lastSeen := mapInt(r, "agent_last_seen", 0)
		if lastSeen <= 0 {
			lastSeen = mapInt(r, "last_check_at", 0)
		}
		if lastSeen <= 0 {
			if _, err := h.db.ExecContext(ctx,
				`UPDATE servers SET last_status = 'offline', last_error = ?, updated_at = ? WHERE id = ?`,
				"尚未收到 Agent 心跳", now, id); err == nil {
				h.recordNodeLog(ctx, id, 0, "agent-offline", "failed", 0, "尚未收到 Agent 心跳", "", "cron")
				offline = append(offline, mapStr(r, "name", ""))
				notices = append(notices, serverStatusNotice{ID: id, Detail: "尚未收到 Agent 心跳"})
			}
			continue
		}
		pollSec := mapInt(r, "agent_next_poll_sec", 60)
		if now-lastSeen <= agentOfflineTimeoutMs(pollSec) {
			continue
		}
		if _, err := h.db.ExecContext(ctx,
			`UPDATE servers SET last_status = 'offline', last_error = ?, updated_at = ? WHERE id = ?`,
			"Agent 心跳超时（"+itoa64((now-lastSeen)/1000)+" 秒未上报）", now, id); err != nil {
			continue
		}
		h.recordNodeLog(ctx, id, 0, "agent-offline", "failed", 0, "agent 心跳超时", "", "cron")
		offline = append(offline, mapStr(r, "name", ""))
		notices = append(notices, serverStatusNotice{ID: id, Detail: fmt.Sprintf("心跳超时（%d 秒未上报）", (now-lastSeen)/1000)})
	}
	if len(offline) > 0 {
		h.log.Info("cron: 标记服务器离线", "count", len(offline), "servers", strings.Join(offline, ","))
		h.notifyStatus(ctx, "服务器离线", notices)
	}
}

type serverStatusNotice struct {
	ID     int64
	Detail string
}

func (h *handlers) notifyStatus(ctx context.Context, title string, notices []serverStatusNotice) {
	if h.db == nil || len(notices) == 0 {
		return
	}
	enabled := strings.TrimSpace(h.getSetting(ctx, "monitor_notify_enabled", "0"))
	if enabled != "1" && !strings.EqualFold(enabled, "true") {
		return
	}
	emails := normalizeEmailList(h.getSetting(ctx, "monitor_notify_emails", ""))
	if emails == "" {
		return
	}
	cooldown := int64(intOrJS(h.getSetting(ctx, "monitor_notify_cooldown_min", "10"), 10, 1, 1440))
	last := int64(intOrJS(h.getSetting(ctx, "monitor_notify_last_at", "0"), 0, 0, 1<<60))
	if nowMillis()-last < cooldown*60_000 {
		return
	}

	var rows strings.Builder
	for _, n := range notices {
		var (
			name, host, ip, ip4, ip6, version, lastErr string
			lastSeen                                   int64
		)
		err := h.db.QueryRowContext(ctx, `SELECT COALESCE(name,''), COALESCE(host,''),
		  COALESCE(last_ip,''), COALESCE(last_ip4,''), COALESCE(last_ip6,''),
		  COALESCE(NULLIF(agent_last_seen,0), last_check_at, 0), COALESCE(agent_version,''), COALESCE(last_error,'')
		  FROM servers WHERE id = ? LIMIT 1`, n.ID).
			Scan(&name, &host, &ip, &ip4, &ip6, &lastSeen, &version, &lastErr)
		if err != nil {
			continue
		}
		addr := firstNonEmpty(ip4, ip, ip6, host, "—")
		lastText := "从未"
		if lastSeen > 0 {
			lastText = time.UnixMilli(lastSeen).Format("2006-01-02 15:04:05") + " UTC"
		}
		detail := firstNonEmpty(n.Detail, lastErr, title)
		fmt.Fprintf(&rows, `<tr>
		  <td style="padding:8px 10px;border-bottom:1px solid #e2e8f0"><b>%s</b></td>
		  <td style="padding:8px 10px;border-bottom:1px solid #e2e8f0">%s</td>
		  <td style="padding:8px 10px;border-bottom:1px solid #e2e8f0">%s</td>
		  <td style="padding:8px 10px;border-bottom:1px solid #e2e8f0">%s</td>
		  <td style="padding:8px 10px;border-bottom:1px solid #e2e8f0">%s</td>
		</tr>`, html.EscapeString(name), html.EscapeString(addr), html.EscapeString(lastText),
			html.EscapeString(version), html.EscapeString(detail))
	}
	if rows.Len() == 0 {
		return
	}

	intro := fmt.Sprintf(`<p style="margin:0 0 12px"><b>%s</b></p>
	  <p style="margin:0 0 12px;color:#64748b">时间：%s<br>数量：%d 台</p>
	  <table style="width:100%%;border-collapse:collapse;font-size:13px">
	    <thead><tr style="background:#f8fafc;color:#475569">
	      <th align="left" style="padding:8px 10px">服务器</th>
	      <th align="left" style="padding:8px 10px">地址</th>
	      <th align="left" style="padding:8px 10px">最后心跳</th>
	      <th align="left" style="padding:8px 10px">Agent</th>
	      <th align="left" style="padding:8px 10px">原因</th>
	    </tr></thead>
	    <tbody>%s</tbody>
	  </table>`, html.EscapeString(title), time.Now().Format("2006-01-02 15:04:05 MST"), len(notices), rows.String())
	if base := h.publicBaseURL(ctx); base != "" {
		intro += fmt.Sprintf(`<p style="margin:16px 0 0"><a href="%s/servers">打开服务器管理</a></p>`, html.EscapeString(base))
	}
	mailBody := emailHTML(title, intro, "", 0)
	subject := fmt.Sprintf("【代理配置管理】%s（%d 台）", title, len(notices))
	sent := false
	for _, to := range strings.Split(emails, ",") {
		if err := h.sendMail(to, subject, mailBody); err == nil {
			sent = true
		}
	}
	if sent {
		_ = h.putSetting(ctx, "monitor_notify_last_at", itoa64(nowMillis()))
	}
}

func (h *handlers) cronTaskMaintenance(ctx context.Context, now int64) {
	if _, err := h.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status = 'pending', attempts = attempts + 1, sent_at = 0
		  WHERE status = 'sent' AND sent_at != 0 AND sent_at < ? AND attempts + 1 < 3`, now-10*60*1000); err != nil {
		h.log.Warn("cron: 回收 sent 任务失败", "err", err)
	}
	_, _ = h.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status = 'failed', finished_at = ?, output = '任务未回执（超过 10 分钟）'
		  WHERE status = 'sent' AND sent_at != 0 AND sent_at < ? AND attempts + 1 >= 3`, now, now-10*60*1000)
	_, _ = h.db.ExecContext(ctx,
		`UPDATE agent_tasks SET status = 'expired', finished_at = ? WHERE status = 'pending' AND created_at < ?`,
		now, now-24*3600*1000)
	// 批量任务锁超时释放（进程崩溃兜底）
	_, _ = h.db.ExecContext(ctx,
		`UPDATE batch_jobs SET locked_at = 0, runner_id = '', status = 'queued', updated_at = ?
		  WHERE status = 'running' AND locked_at > 0 AND locked_at < ?`, now, now-5*60*1000)
}

func (h *handlers) cronResumeJobs(ctx context.Context, now int64) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, COALESCE(actor,'system') FROM batch_jobs WHERE status = 'queued' AND created_at < ? ORDER BY id ASC LIMIT 2`,
		now-30_000)
	if err != nil {
		return
	}
	for _, r := range rows {
		id := mapInt(r, "id", 0)
		if id <= 0 {
			continue
		}
		h.startBatchJob(id, mapStr(r, "actor", "system"))
	}
}

func (h *handlers) cronCleanup(ctx context.Context, now int64) {
	lastClean := int64(intOrJS(h.getSetting(ctx, "batch_cleanup_at", "0"), 0, 0, 1<<60))
	if now-lastClean < 10*60*1000 {
		return
	}
	batchDays := int64(intOrJS(h.getSetting(ctx, "batch_retention_days", "7"), 7, 1, 365))
	taskDays := int64(intOrJS(h.getSetting(ctx, "agent_task_retention_days", "7"), 7, 1, 365))
	metricsDays := int64(intOrJS(h.getSetting(ctx, "metrics_retention_days", "7"), 7, 1, 365))

	batchCutoff := now - batchDays*24*3600*1000
	_, _ = h.db.ExecContext(ctx,
		`DELETE bi FROM batch_job_items bi JOIN batch_jobs bj ON bj.id = bi.job_id
		  WHERE bj.finished_at > 0 AND bj.finished_at < ?`, batchCutoff)
	_, _ = h.db.ExecContext(ctx, `DELETE FROM batch_jobs WHERE finished_at > 0 AND finished_at < ?`, batchCutoff)

	taskCutoff := now - taskDays*24*3600*1000
	_, _ = h.db.ExecContext(ctx,
		`DELETE FROM agent_tasks WHERE status IN ('success','failed','expired') AND finished_at > 0 AND finished_at < ? LIMIT 2000`, taskCutoff)

	metricsCutoff := now - metricsDays*24*3600*1000
	_, _ = h.db.ExecContext(ctx, `DELETE FROM server_metrics WHERE created_at > 0 AND created_at < ? LIMIT 5000`, metricsCutoff)

	// V15.6：用户流量小时聚合保留 90 天；连接快照只留最近 1 天
	trafficDays := int64(intOrJS(h.getSetting(ctx, "traffic_retention_days", "90"), 90, 7, 3650))
	h.pruneUserTraffic(ctx, trafficDays)

	_ = h.putSetting(ctx, "batch_cleanup_at", itoa64(now))
}

func (h *handlers) cronUUIDReconcile(ctx context.Context, now int64) {
	every := int64(intOrJS(h.getSetting(ctx, "uuid_reconcile_interval_min", "30"), 30, 5, 1440))
	last := int64(intOrJS(h.getSetting(ctx, "uuid_reconcile_last_at", "0"), 0, 0, 1<<60))
	if now-last < every*60_000 {
		return
	}
	var n int64
	if err := h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM singbox_nodes`).Scan(&n); err != nil || n == 0 {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	r := h.reconcileSingboxUUIDState(cctx, "cron", true)
	if r.Error != "" {
		h.log.Warn("cron: UUID 巡检失败", "err", r.Error)
		return
	}
	h.log.Info("cron: UUID 巡检完成", "checked", r.Checked, "drifted", r.Drifted, "fixed", r.Fixed)
}
