package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type jobItem struct {
	Target   string
	TargetID int64
	Label    string
}

type jobKind struct {
	Title     func(params map[string]any, n int) string
	EmptyHint string
	Items     func(h *handlers, ctx context.Context, params map[string]any) ([]jobItem, error)
	Run       func(h *handlers, ctx context.Context, params map[string]any, item jobItem, actor string) (bool, string)
}

var batchKinds = map[string]jobKind{
	"singbox.apply_all": {
		Title: func(_ map[string]any, n int) string {
			return "一键下发全部节点（" + strconv.Itoa(n) + " 个）"
		},
		EmptyHint: "没有已绑定的 sing-box 节点",
		Items: func(h *handlers, ctx context.Context, _ map[string]any) ([]jobItem, error) {
			rows, err := queryRowsAsMaps(ctx, h.db, `SELECT id, name FROM singbox_nodes ORDER BY id ASC`)
			if err != nil {
				return nil, err
			}
			out := make([]jobItem, 0, len(rows))
			for _, r := range rows {
				out = append(out, jobItem{Target: "singbox_node", TargetID: mapInt(r, "id", 0), Label: mapStr(r, "name", "")})
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, params map[string]any, item jobItem, actor string) (bool, string) {
			force, _ := params["force"].(bool)
			doPush := true
			if v, ok := params["push"].(bool); ok {
				doPush = v
			}
			files, _, users, changed, msg := h.applySingboxNode(ctx, item.TargetID, nil, force)
			if msg != "" {
				return false, msg
			}
			queued := 0
			if doPush {
				for _, q := range h.queueSingboxDeploy(ctx, files, actor) {
					if ok, _ := q["ok"].(bool); ok {
						queued++
					}
				}
			}
			b, _ := json.Marshal(map[string]any{"changed": changed, "queued": queued, "users": len(users)})
			return true, "变更 " + strconv.Itoa(changed) + " 个文件，排队 " + strconv.Itoa(queued) + " 条 " + string(b)
		},
	},
	"singbox.push": {
		Title: func(_ map[string]any, n int) string {
			return "批量授权推送（" + strconv.Itoa(n) + " 台服务器）"
		},
		EmptyHint: "请选择服务器",
		Items: func(h *handlers, ctx context.Context, params map[string]any) ([]jobItem, error) {
			out := []jobItem{}
			for _, v := range jsonSlice(params["server_ids"]) {
				id := jsonInt(v)
				if id > 0 {
					out = append(out, jobItem{Target: "server", TargetID: id, Label: "server#" + strconv.FormatInt(id, 10)})
				}
			}
			return out, nil
		},
		Run: func(h *handlers, ctx context.Context, params map[string]any, item jobItem, actor string) (bool, string) {
			var uuids []string
			for _, v := range jsonSlice(params["uuids"]) {
				if s := strings.TrimSpace(jsonStr(v)); s != "" {
					uuids = append(uuids, s)
				}
			}
			if len(uuids) == 0 {
				return false, "缺少 uuids"
			}
			nodeID, _, msg := h.ensureSingboxNodeForServer(ctx, item.TargetID)
			if msg != "" {
				return false, msg
			}
			tpl, _, _, _, msg := h.loadSingboxNodeFull(ctx, nodeID)
			if msg != "" {
				return false, msg
			}
			users := singboxUsersFromUUIDs(tpl, uuids)
			files, _, outUsers, changed, msg := h.applySingboxNode(ctx, nodeID, users, false)
			if msg != "" {
				return false, msg
			}
			uuidJSON, _ := json.Marshal(uuids)
			_, _ = h.db.ExecContext(ctx, `UPDATE singbox_nodes SET uuid_list = ?, updated_at = ? WHERE id = ?`,
				string(uuidJSON), nowMillis(), nodeID)
			queued := 0
			doPush := true
			if v, ok := params["push"].(bool); ok {
				doPush = v
			}
			if doPush {
				for _, q := range h.queueSingboxDeploy(ctx, files, actor) {
					if ok, _ := q["ok"].(bool); ok {
						queued++
					}
				}
			}
			return true, "用户 " + strconv.Itoa(len(outUsers)) + "，变更 " + strconv.Itoa(changed) + "，排队 " + strconv.Itoa(queued)
		},
	},
}

type batchJobJSON struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Params     any    `json:"params"`
	Status     string `json:"status"`
	Total      int64  `json:"total"`
	Done       int64  `json:"done"`
	Failed     int64  `json:"failed"`
	Cursor     int64  `json:"cursor"`
	Actor      string `json:"actor"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	RunnerID   string `json:"runner_id"`
}

func batchJobToJSON(row map[string]any) batchJobJSON {
	var params any = map[string]any{}
	if raw := mapStr(row, "params", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &params)
	}
	return batchJobJSON{
		ID: mapInt(row, "id", 0), Kind: mapStr(row, "kind", ""), Title: mapStr(row, "title", ""),
		Params: params, Status: mapStr(row, "status", ""), Total: mapInt(row, "total", 0),
		Done: mapInt(row, "done", 0), Failed: mapInt(row, "failed", 0), Cursor: mapInt(row, "cursor", 0),
		Actor: mapStr(row, "actor", ""), CreatedAt: mapInt(row, "created_at", 0),
		UpdatedAt: mapInt(row, "updated_at", 0), StartedAt: mapInt(row, "started_at", 0),
		FinishedAt: mapInt(row, "finished_at", 0), RunnerID: mapStr(row, "runner_id", ""),
	}
}

func batchItemToJSON(row map[string]any) map[string]any {
	var detail any = map[string]any{}
	if raw := mapStr(row, "detail", ""); raw != "" {
		_ = json.Unmarshal([]byte(raw), &detail)
	}
	return map[string]any{
		"id": mapInt(row, "id", 0), "seq": mapInt(row, "seq", 0),
		"target": mapStr(row, "target", ""), "target_id": mapInt(row, "target_id", 0),
		"label": mapStr(row, "label", ""), "status": mapStr(row, "status", ""),
		"message": mapStr(row, "message", ""), "detail": detail,
		"attempts": mapInt(row, "attempts", 0), "started_at": mapInt(row, "started_at", 0),
		"finished_at": mapInt(row, "finished_at", 0),
	}
}

func (h *handlers) batchCreateJob(ctx context.Context, kind string, params map[string]any, actor string) (int64, int, bool, string) {
	def, ok := batchKinds[kind]
	if !ok {
		return 0, 0, false, "不支持的任务类型：" + kind
	}
	paramsText, _ := json.Marshal(params)
	var dupID, dupTotal int64
	err := h.db.QueryRowContext(ctx, `SELECT id, total FROM batch_jobs WHERE kind = ? AND params = ?
	      AND status IN ('queued','running') AND created_at >= ? LIMIT 1`,
		kind, string(paramsText), nowMillis()-10*60*1000).Scan(&dupID, &dupTotal)
	if err == nil {
		return dupID, int(dupTotal), true, ""
	}
	items, err := def.Items(h, ctx, params)
	if err != nil {
		return 0, 0, false, "生成任务项失败：" + err.Error()
	}
	if len(items) == 0 {
		return 0, 0, false, def.EmptyHint
	}
	if len(items) > 500 {
		return 0, 0, false, "目标过多（" + strconv.Itoa(len(items)) + "），单任务上限 500，请分批执行"
	}
	title := def.Title(params, len(items))
	if len(title) > 120 {
		title = title[:120]
	}
	dedupKey := batchJobDedupKey(kind, string(paramsText))
	now := nowMillis()
	var jobID int64
	for attempt := 0; attempt < 2 && jobID == 0; attempt++ {
		key := dedupKey
		if attempt > 0 {
			// 撞到「终态任务残留 dedup_key」的历史脏数据：换一个一次性键重新插入。
			// 不能把已结束的旧任务当成本次创建结果返回，否则任务永远不执行（审计 P1-1）。
			key = batchJobDedupKey(kind, string(paramsText)+"#"+strconv.FormatInt(now, 10)+"-"+strconv.Itoa(attempt))
		}
		res, err := h.db.ExecContext(ctx, "INSERT INTO batch_jobs (kind, title, params, dedup_key, status, total, done, failed, `cursor`, actor, created_at, updated_at)\n\t  VALUES (?, ?, ?, ?, 'queued', ?, 0, 0, 0, ?, ?, ?) ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)", kind, title, string(paramsText), key, len(items), actor, now, now)
		if err != nil {
			return 0, 0, false, "创建任务失败：" + err.Error()
		}
		if n, _ := res.RowsAffected(); n == 0 {
			var dupID, dupTotal int64
			var dupStatus string
			if qerr := h.db.QueryRowContext(ctx, "SELECT id, total, COALESCE(status,'') FROM batch_jobs WHERE dedup_key = ? LIMIT 1", key).
				Scan(&dupID, &dupTotal, &dupStatus); qerr == nil {
				// 只有仍在排队 / 执行中的同一任务才算重复；终态残留键换新键重试。
				if dupStatus == "queued" || dupStatus == "running" {
					return dupID, int(dupTotal), true, ""
				}
			}
			continue
		}
		jobID, _ = res.LastInsertId()
	}
	if jobID == 0 {
		return 0, 0, false, "创建任务失败：去重键冲突，请稍后重试"
	}
	for i, it := range items {
		_, err := h.db.ExecContext(ctx, `INSERT INTO batch_job_items (job_id, seq, target, target_id, label, status, attempts, started_at)
		  VALUES (?, ?, ?, ?, ?, 'pending', 0, 0)`, jobID, i+1, it.Target, it.TargetID, truncateRunes(it.Label, 120))
		if err != nil {
			return 0, 0, false, "创建任务项失败：" + err.Error()
		}
	}
	h.recordNodeLog(ctx, 0, 0, "batch-job", "success", 0, "创建后台任务 #"+strconv.FormatInt(jobID, 10)+"："+title, "", actor)
	return jobID, len(items), false, ""
}

// batchJobTerminalStatuses 批量任务终态集合。
//
// 进入终态时必须清空 dedup_key：uk_batch_jobs_dedup 的唯一键以前只在「正常跑完」
// 这一条出口清理，取消 / 失败 / 未知类型等分支都会把 sha256(kind|params) 永久占住，
// 同参数任务随后永远被判重复（审计 P1-1）。
const batchJobTerminalStatuses = "'success','partial','failed','canceled'"

const (
	// batchJobLockTimeoutMS 执行中锁的失效阈值：执行器每 batchJobHeartbeatIntervalMS
	// 续期一次 locked_at，cron 只有在这个阈值内没收到续期才判定进程已挂（审计 P1-2）。
	batchJobLockTimeoutMS = 5 * 60 * 1000
	// batchJobHeartbeatIntervalMS 默认心跳间隔：必须显著小于 batchJobLockTimeoutMS。
	batchJobHeartbeatIntervalMS = 60 * 1000
)

var (
	// batchJobHeartbeatInterval 独立成变量便于测试缩短；生产恒为默认值。
	batchJobHeartbeatInterval = batchJobHeartbeatIntervalMS
	batchRunnerSeq            atomic.Int64
)

// batchJobDedupKey 批量任务去重键：sha256(kind|params)（与 0021 迁移的唯一索引口径一致）。
func batchJobDedupKey(kind, paramsText string) string {
	sum := sha256.Sum256([]byte(kind + "|" + paramsText))
	return hex.EncodeToString(sum[:])
}

// newBatchRunnerID 生成本次执行唯一的 runner 标识。
//
// 旧实现固定写 "go"，无法区分「同一任务的两个执行者」；cron 回收后重新抢占时
// 旧 runner 也无法发现自己已失去锁（审计 P1-2）。
func newBatchRunnerID() string {
	return "go-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatInt(batchRunnerSeq.Add(1), 10)
}

// batchJobOwned 判断 job 的锁是否仍属于 runnerID（被取消或被 cron 接管后返回 false）。
// 查询失败时保守认为仍持有锁，避免瞬时数据库抖动让已开始的批量任务半途放弃。
func (h *handlers) batchJobOwned(ctx context.Context, jobID int64, runnerID string) bool {
	var status, runner string
	if err := h.db.QueryRowContext(ctx,
		`SELECT COALESCE(status,''), COALESCE(runner_id,'') FROM batch_jobs WHERE id = ?`, jobID).
		Scan(&status, &runner); err != nil {
		return true
	}
	return status == "running" && runner == runnerID
}

// heartbeatBatchJob 续期 locked_at；返回 false 表示锁已被取消 / 被接管。
func (h *handlers) heartbeatBatchJob(ctx context.Context, jobID int64, runnerID string) bool {
	now := nowMillis()
	res, err := h.db.ExecContext(ctx,
		`UPDATE batch_jobs SET locked_at = ?, updated_at = ? WHERE id = ? AND status = 'running' AND runner_id = ?`,
		now, now, jobID, runnerID)
	if err != nil {
		return true // 数据库抖动：交给下一轮心跳重试
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return true
	}
	return h.batchJobOwned(ctx, jobID, runnerID)
}

// startBatchJobHeartbeat 起一个续期协程，返回「停止并等待退出」的函数。
//
// 单项执行时间可能超过锁阈值（例如 warp.update 每台最多等 5 分钟回执），
// 所以续期必须是并发的，不能只在两项之间刷一次 locked_at。
func (h *handlers) startBatchJobHeartbeat(ctx context.Context, jobID int64, runnerID string) func() {
	interval := time.Duration(batchJobHeartbeatInterval) * time.Millisecond
	if interval <= 0 {
		interval = time.Duration(batchJobHeartbeatIntervalMS) * time.Millisecond
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !h.heartbeatBatchJob(ctx, jobID, runnerID) {
					return
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

// finishBatchJob 把任务置为终态，并清空 dedup_key（审计 P1-1）。
// 只更新仍由本 runner 持有的任务，避免覆盖「已被取消 / 已被接管」的状态。
func (h *handlers) finishBatchJob(ctx context.Context, jobID int64, runnerID, status string) bool {
	now := nowMillis()
	res, err := h.db.ExecContext(ctx,
		`UPDATE batch_jobs SET status = ?, locked_at = 0, runner_id = '', finished_at = ?, updated_at = ?, dedup_key = NULL
		  WHERE id = ? AND runner_id = ?`,
		status, now, now, jobID, runnerID)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

var batchRunSem = make(chan struct{}, 2)

// startBatchJob 后台执行批量任务（替代 Worker Queue/DO 调度）。
func (h *handlers) startBatchJob(jobID int64, actor string) {
	go func() {
		batchRunSem <- struct{}{}
		defer func() { <-batchRunSem }()
		h.runBatchJobInline(context.Background(), jobID, actor)
	}()
}

// runBatchJobInline 同步执行任务（迁移期替代 Worker 的 Queue/DO 调度）。
func (h *handlers) runBatchJobInline(ctx context.Context, jobID int64, actor string) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT * FROM batch_jobs WHERE id = ? LIMIT 1`, jobID)
	if err != nil || len(rows) == 0 {
		return
	}
	job := rows[0]
	if mapStr(job, "status", "") == "canceled" {
		return
	}
	// 先抢占：只有 queued 才能执行，避免 cron 恢复与 API 触发重复跑同一任务。
	// runner_id 每轮唯一：cron 回收后重跑时，旧 runner 靠它发现自己已失去锁。
	runnerID := newBatchRunnerID()
	now := nowMillis()
	claim, err := h.db.ExecContext(ctx, `UPDATE batch_jobs SET status = 'running', locked_at = ?, runner_id = ?,
	  started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END, updated_at = ? WHERE id = ? AND status = 'queued'`,
		now, runnerID, now, now, jobID)
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n == 0 {
		return
	}
	// 执行期间并发续期 locked_at：单项可能阻塞数分钟（等 Agent 回执），
	// 只在两项之间续期仍然会被 cron 误判为崩溃并启动第二个 runner（审计 P1-2）。
	stopHeartbeat := h.startBatchJobHeartbeat(ctx, jobID, runnerID)
	defer stopHeartbeat()

	kind := mapStr(job, "kind", "")
	def, ok := batchKinds[kind]
	if !ok {
		h.finishBatchJob(ctx, jobID, runnerID, "failed")
		return
	}
	params := map[string]any{}
	_ = json.Unmarshal([]byte(mapStr(job, "params", "{}")), &params)

	itemRows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT * FROM batch_job_items WHERE job_id = ? AND status = 'pending' ORDER BY seq ASC`, jobID)
	if err != nil {
		return
	}
	done, failed := int64(0), int64(0)
	for _, itemRow := range itemRows {
		// 每项开始前确认锁仍属于自己：任务被取消或被 cron 接管后立即退出，
		// 避免两个 runner 拿着同一份 pending 快照重复执行（审计 P1-2）。
		if !h.batchJobOwned(ctx, jobID, runnerID) {
			return
		}
		item := jobItem{Target: mapStr(itemRow, "target", ""), TargetID: mapInt(itemRow, "target_id", 0), Label: mapStr(itemRow, "label", "")}
		ok := false
		message := ""
		attempts := int64(0)
		for try := 1; try <= 2; try++ {
			attempts = int64(try)
			_, _ = h.db.ExecContext(ctx, `UPDATE batch_job_items SET status = 'running', attempts = ?, started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END WHERE id = ?`,
				attempts, nowMillis(), mapInt(itemRow, "id", 0))
			ok, message = def.Run(h, ctx, params, item, actor)
			if ok {
				break
			}
		}
		finished := nowMillis()
		status := "failed"
		if ok {
			status = "success"
			done++
		} else {
			failed++
		}
		_, _ = h.db.ExecContext(ctx, `UPDATE batch_job_items SET status = ?, message = ?, finished_at = ? WHERE id = ?`,
			status, truncateRunes(message, 500), finished, mapInt(itemRow, "id", 0))
		_, _ = h.db.ExecContext(ctx, "UPDATE batch_jobs SET done = done + 1, failed = failed + ?, `cursor` = ?, updated_at = ? WHERE id = ?",
			map[bool]int64{true: 0, false: 1}[ok], mapInt(itemRow, "seq", 0), finished, jobID)
	}
	finalStatus := "success"
	if failed > 0 {
		if failed >= int64(len(itemRows)) {
			finalStatus = "failed"
		} else {
			finalStatus = "partial"
		}
	}
	// 结束前先停掉续期协程，避免它和终态更新互相覆盖。
	stopHeartbeat()
	if !h.finishBatchJob(ctx, jobID, runnerID, finalStatus) {
		// 锁已不属于自己（被取消 / 被接管）：不写终态、不记成功日志。
		return
	}
	h.recordNodeLog(ctx, 0, 0, "batch-job", map[bool]string{true: "success", false: "failed"}[finalStatus == "success"],
		0, "后台任务 #"+strconv.FormatInt(jobID, 10)+" 结束："+finalStatus+"（成功 "+strconv.FormatInt(done, 10)+"/"+strconv.Itoa(len(itemRows))+"）", "", "system")
}

// GET /api/jobs
func (h *handlers) jobsList(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := clampInt(int64(intOrJS(r.URL.Query().Get("limit"), 20, 1, 100)), 1, 100)
	where := ""
	args := []any{}
	switch {
	case status == "active":
		where = " WHERE status IN ('queued','running')"
	case status != "" && status != "all":
		where = " WHERE status = ?"
		args = append(args, status)
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM batch_jobs`+where+` ORDER BY id DESC LIMIT ?`,
		append(args, limit)...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "查询任务失败")
		return
	}
	data := make([]batchJobJSON, 0, len(rows))
	for _, row := range rows {
		data = append(data, batchJobToJSON(row))
	}
	var active int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM batch_jobs WHERE status IN ('queued','running')`).Scan(&active)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": data, "active": active})
}

// POST /api/jobs
func (h *handlers) jobsCreate(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "无效 JSON")
		return
	}
	kind := strings.TrimSpace(mapStr(body, "kind", ""))
	params := map[string]any{}
	if v, ok := body["params"].(map[string]any); ok {
		params = v
	}
	jobID, total, duplicate, msg := h.batchCreateJob(r.Context(), kind, params, "admin")
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !duplicate {
		h.startBatchJob(jobID, "admin")
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "async": true, "job_id": jobID, "total": total, "duplicate": duplicate})
}

// GET /api/jobs/{id}
func (h *handlers) jobsGet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	rows, err := queryRowsAsMaps(r.Context(), h.db, `SELECT * FROM batch_jobs WHERE id = ? LIMIT 1`, id)
	if err != nil || len(rows) == 0 {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	out := map[string]any{"success": true, "data": batchJobToJSON(rows[0])}
	if r.URL.Query().Get("items") == "1" {
		offset := intOrJS(r.URL.Query().Get("offset"), 0, 0, 100000)
		limit := intOrJS(r.URL.Query().Get("limit"), 50, 1, 200)
		items, err := queryRowsAsMaps(r.Context(), h.db,
			`SELECT * FROM batch_job_items WHERE job_id = ? ORDER BY seq ASC LIMIT ? OFFSET ?`, id, limit, offset)
		if err == nil {
			list := make([]map[string]any, 0, len(items))
			for _, it := range items {
				list = append(list, batchItemToJSON(it))
			}
			out["items"] = list
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// POST /api/jobs/{id}/cancel
func (h *handlers) jobsCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	var status string
	if err := h.db.QueryRowContext(r.Context(), `SELECT COALESCE(status,'') FROM batch_jobs WHERE id = ?`, id).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	switch status {
	case "success", "partial", "failed", "canceled":
		writeError(w, http.StatusBadRequest, "任务已结束")
		return
	}
	now := nowMillis()
	// 取消也是终态：必须清 dedup_key，否则同参数任务之后会一直被判定为重复（审计 P1-1）。
	_, _ = h.db.ExecContext(r.Context(), `UPDATE batch_jobs SET status = 'canceled', locked_at = 0, runner_id = '', dedup_key = NULL, finished_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	_, _ = h.db.ExecContext(r.Context(), `UPDATE batch_job_items SET status = 'skipped' WHERE job_id = ? AND status = 'pending'`, id)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "已取消（当前执行中的项会跑完）"})
}

// POST /api/jobs/{id}/retry
func (h *handlers) jobsRetry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	var exists int64
	if err := h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM batch_jobs WHERE id = ?`, id).Scan(&exists); err != nil || exists == 0 {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	var failedCount int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM batch_job_items WHERE job_id = ? AND status = 'failed'`, id).Scan(&failedCount)
	if failedCount == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "retry_items": 0})
		return
	}
	now := nowMillis()
	_, _ = h.db.ExecContext(r.Context(), `UPDATE batch_job_items SET status = 'pending', message = '', attempts = 0, started_at = 0, finished_at = 0 WHERE job_id = ? AND status = 'failed'`, id)
	_, _ = h.db.ExecContext(r.Context(), "UPDATE batch_jobs SET status = 'queued', `cursor` = 0, done = done - ?, failed = 0, locked_at = 0, runner_id = '', finished_at = 0, updated_at = ? WHERE id = ?",
		failedCount, now, id)
	h.startBatchJob(id, "admin")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "retry_items": failedCount})
}

// POST /api/jobs/clear —— 一键清理已结束的后台任务（成功/失败/部分失败/已取消），运行中的保留。
func (h *handlers) jobsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
		return
	}
	const doneCond = `status IN (` + batchJobTerminalStatuses + `)`
	itemRes, _ := h.db.ExecContext(r.Context(),
		`DELETE FROM batch_job_items WHERE job_id IN (SELECT id FROM batch_jobs WHERE `+doneCond+`)`)
	jobRes, err := h.db.ExecContext(r.Context(), `DELETE FROM batch_jobs WHERE `+doneCond)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "清理失败")
		return
	}
	deleted, _ := jobRes.RowsAffected()
	items, _ := itemRes.RowsAffected()
	var active int64
	_ = h.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM batch_jobs WHERE status IN ('queued','running')`).Scan(&active)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "deleted": deleted, "items_deleted": items, "active": active,
		"message": "已清理 " + itoa64(deleted) + " 个已结束任务",
	})
}
