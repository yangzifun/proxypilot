package api

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"time"

	"github.com/yzfn/proxypilot/server/internal/config"
)

// 节点配置自愈：定期核对「机器上的真实配置」与「面板里的内容」，不一致就重下发。
//
// 为什么需要（2026-11-12 曼谷1 故障）：
//   Agent 安装脚本会用 bootstrap 默认模板覆盖 sb.json；而面板的 applied_version
//   还是旧值，不会自动重下发。表现为 sing-box active 但少了好几个入站，
//   面板「看起来正常」，没人会发现。只靠版本号无法察觉，必须比对真实文件的 sha256。
//
// 设计取舍：
//   1. 复用已有的 inspect 能力（probeNodeConfigs 会排队 inspect 任务并等回执），
//      不新增协议字段；
//   2. 巡检节奏由 node_configs.last_checked_at 控制（默认 30 分钟一次），
//      避免每轮 15 秒的 cron 都去排队任务把队列打满；
//   3. 自愈带冷却（默认 10 分钟一次），防止「下发-被覆盖-再下发」的抖动；
//   4. 只处理 enabled 且在线的节点，离线节点等它回来再查。

const (
	configCheckIntervalMS = 30 * 60 * 1000 // 每 30 分钟核对一次
	configHealCooldownMS  = 10 * 60 * 1000 // 同一配置 10 分钟内最多自愈一次
	configCheckBatch      = 8              // 单轮最多核对几台，避免一次排队太多任务
)

// cronConfigSelfHeal 由 cron 调用；内部按 last_checked_at 自行节流。
func (h *handlers) cronConfigSelfHeal(ctx context.Context, now int64) {
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT c.id, c.server_id, c.name, c.target_path, c.content_hash, c.last_checked_at, c.last_heal_at
		   FROM node_configs c JOIN servers s ON s.id = c.server_id
		  WHERE c.enabled = 1 AND s.enabled = 1
		    AND (c.last_checked_at = 0 OR c.last_checked_at < ?)
		    AND COALESCE(s.last_status,'') <> 'offline'
		  ORDER BY c.last_checked_at ASC LIMIT ?`,
		now-configCheckIntervalMS, configCheckBatch)
	if err != nil || len(rows) == 0 {
		return
	}

	probes := h.probeNodeConfigs(ctx, rows)
	for id, p := range probes {
		healAt := mapInt64FromRows(rows, id, "last_heal_at")
		if p.Err != "" {
			// 读不到（文件缺失/网络问题）也算核对过，避免每轮重试把队列打满；
			// 下发给 Agent 的 inspect 已经带了错误原因，排查看 node_push_logs。
			_, _ = h.db.ExecContext(ctx, `UPDATE node_configs SET last_checked_at = ? WHERE id = ?`, now, id)
			continue
		}
		want := strings.ToLower(strings.TrimSpace(mapStrFromRows(rows, id, "content_hash")))
		got := strings.ToLower(strings.TrimSpace(p.Sha256))
		_, _ = h.db.ExecContext(ctx,
			`UPDATE node_configs SET last_checked_at = ?, last_check_sha = ? WHERE id = ?`, now, got, id)

		if want == "" || got == "" || want == got {
			continue
		}
		if healAt != 0 && now-healAt < configHealCooldownMS {
			continue
		}
		h.log.Warn("节点配置与面板不一致，自动重下发",
			"node_config", id, "server", p.ServerID, "want", want, "got", got)
		h.autoPushNodeConfig(ctx, p.ServerID, id)
		_, _ = h.db.ExecContext(ctx,
			`UPDATE node_configs SET last_heal_at = ? WHERE id = ?`, now, id)
	}
}

// autoPushNodeConfig 把指定节点的配置重新下发一次（复用原生 deploy 通道）。
//
// 复用 nodeConfigPush 那条路的组件：loadNodeConfig + agentBuildDeployPayload + agentQueueTask，
// 保证自愈下发的载荷和人工点「下发」完全一致。
func (h *handlers) autoPushNodeConfig(ctx context.Context, serverID, configID int64) {
	cfg, err := h.loadNodeConfig(ctx, configID)
	if err != nil || cfg == nil {
		h.log.Warn("自愈下发失败：读取配置出错", "config", configID, "err", err)
		return
	}
	srv, err := h.fetchAgentServer(ctx, cfg.ServerID)
	if err != nil || srv == nil {
		h.log.Warn("自愈下发失败：服务器不存在", "config", configID, "err", err)
		return
	}
	payload := h.agentBuildDeployPayload(ctx, cfg, srv)
	taskID, dup, msg := h.agentQueueTask(ctx, srv, "deploy", cfg.ID, payload, "system")
	if msg != "" {
		h.log.Warn("自愈下发失败：排队出错", "config", configID, "err", msg)
		return
	}
	h.recordNodeLog(ctx, configID, serverID, "auto-heal", "queued", 0,
		"面板核对发现机器上的配置与内容不一致，已自动重下发（任务 "+intToStr(taskID)+"）", "", "system")
	_, _ = dup, srv
}

// 小工具：从按行结果里取字段（避免每处都写 mapStr 判空）。
func mapStrFromRows(rows []map[string]any, id int64, key string) string {
	for _, r := range rows {
		if mapInt(r, "id", 0) == id {
			return mapStr(r, key, "")
		}
	}
	return ""
}

func mapInt64FromRows(rows []map[string]any, id int64, key string) int64 {
	for _, r := range rows {
		if mapInt(r, "id", 0) == id {
			return mapInt(r, key, 0)
		}
	}
	return 0
}
func intToStr(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
// StartConfigSelfHeal 以独立协程做配置自愈巡检。
//
// 不放在 runCronOnce 里：probeNodeConfigs 要等 Agent 回执（最长 100 秒），
// 放进 cron 会把离线判定、任务回收等准时任务一起拖慢。
func StartConfigSelfHeal(ctx context.Context, cfg config.Config, db *sql.DB, log *slog.Logger) {
	if db == nil {
		return
	}
	h := &handlers{cfg: cfg, db: db, log: log}
	go func() {
		// 启动后先等 3 分钟，避开面板刚重启时的任务高峰
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Minute):
		}
		h.cronConfigSelfHeal(ctx, nowMillis())
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.cronConfigSelfHeal(ctx, nowMillis())
			}
		}
	}()
}