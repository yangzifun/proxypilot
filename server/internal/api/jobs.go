package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
	now := nowMillis()
	res, err := h.db.ExecContext(ctx, "INSERT INTO batch_jobs (kind, title, params, status, total, done, failed, `cursor`, actor, created_at, updated_at)\n\t  VALUES (?, ?, ?, 'queued', ?, 0, 0, 0, ?, ?, ?)", kind, title, string(paramsText), len(items), actor, now, now)
	if err != nil {
		return 0, 0, false, "创建任务失败：" + err.Error()
	}
	jobID, _ := res.LastInsertId()
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
	// 先抢占：只有 queued 才能执行，避免 cron 恢复与 API 触发重复跑同一任务
	claim, err := h.db.ExecContext(ctx, `UPDATE batch_jobs SET status = 'running', locked_at = ?, runner_id = 'go',
	  started_at = CASE WHEN started_at = 0 THEN ? ELSE started_at END, updated_at = ? WHERE id = ? AND status = 'queued'`,
		nowMillis(), nowMillis(), nowMillis(), jobID)
	if err != nil {
		return
	}
	if n, _ := claim.RowsAffected(); n == 0 {
		return
	}
	kind := mapStr(job, "kind", "")
	def, ok := batchKinds[kind]
	if !ok {
		_, _ = h.db.ExecContext(ctx, `UPDATE batch_jobs SET status = 'failed', finished_at = ?, updated_at = ? WHERE id = ?`, nowMillis(), nowMillis(), jobID)
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
		var jobStatus string
		_ = h.db.QueryRowContext(ctx, `SELECT COALESCE(status,'') FROM batch_jobs WHERE id = ?`, jobID).Scan(&jobStatus)
		if jobStatus == "canceled" {
			break
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
	now := nowMillis()
	_, _ = h.db.ExecContext(ctx, `UPDATE batch_jobs SET status = ?, locked_at = 0, runner_id = '', finished_at = ?, updated_at = ? WHERE id = ?`,
		finalStatus, now, now, jobID)
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
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "job_id": jobID, "total": total, "duplicate": duplicate})
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
	_, _ = h.db.ExecContext(r.Context(), `UPDATE batch_jobs SET status = 'canceled', locked_at = 0, runner_id = '', finished_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
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
