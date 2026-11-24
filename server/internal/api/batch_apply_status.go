package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
)

const batchApplyTimeoutMs = int64(120 * 1000)

type batchApplyItemDetail struct {
	NodeID      int64             `json:"node_id"`
	TaskIDs     []int64           `json:"task_ids"`
	TaskTotal   int               `json:"task_total"`
	TaskResults map[string]bool   `json:"task_results,omitempty"`
	TaskErrors  map[string]string `json:"task_errors,omitempty"`
	QueueErrors []string          `json:"queue_errors,omitempty"`
	HardFail    bool              `json:"hard_fail,omitempty"`
	QueuedAt    int64             `json:"queued_at,omitempty"`
	Changed     int               `json:"changed,omitempty"`
	Users       int               `json:"users,omitempty"`
	Timeout     bool              `json:"timeout,omitempty"`
}

// recordBatchApplyTaskResult 在 Agent deploy 任务终态回执后，回填对应批任务的逐节点状态。
// 一个批任务项可能对应多个 Agent 任务（一个节点多个配置文件），需全部回执才算终态。
func (h *handlers) recordBatchApplyTaskResult(ctx context.Context, taskID int64, ok bool, output string) {
	if h.db == nil || taskID <= 0 {
		return
	}
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, job_id, COALESCE(detail,'') AS detail
		   FROM batch_job_items
		  WHERE status = 'queued' AND target = 'singbox_node' LIMIT 500`)
	if err != nil {
		return
	}
	for _, row := range rows {
		itemID := mapInt(row, "id", 0)
		jobID := mapInt(row, "job_id", 0)
		var d batchApplyItemDetail
		if json.Unmarshal([]byte(mapStr(row, "detail", "{}")), &d) != nil || len(d.TaskIDs) == 0 {
			continue
		}
		matched := false
		for _, id := range d.TaskIDs {
			if id == taskID {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if d.TaskResults == nil {
			d.TaskResults = map[string]bool{}
		}
		if d.TaskErrors == nil {
			d.TaskErrors = map[string]string{}
		}
		key := strconv.FormatInt(taskID, 10)
		if _, seen := d.TaskResults[key]; seen {
			continue
		}
		d.TaskResults[key] = ok
		if !ok {
			d.TaskErrors[key] = truncateRunes(strings.TrimSpace(output), 300)
		}
		detailJSON, _ := json.Marshal(d)
		if len(d.TaskResults) < len(d.TaskIDs) {
			_, _ = h.db.ExecContext(ctx,
				`UPDATE batch_job_items SET detail = ? WHERE id = ? AND status = 'queued'`, string(detailJSON), itemID)
			continue
		}
		failed := d.HardFail
		firstErr := ""
		if len(d.QueueErrors) > 0 {
			failed = true
			firstErr = strings.Join(d.QueueErrors, "；")
		}
		for _, id := range d.TaskIDs {
			k := strconv.FormatInt(id, 10)
			if !d.TaskResults[k] {
				failed = true
				if firstErr == "" {
					firstErr = d.TaskErrors[k]
				}
			}
		}
		status := "success"
		message := "Agent 部署成功（" + strconv.Itoa(len(d.TaskIDs)) + " 个任务）"
		if failed {
			status = "failed"
			message = "Agent 部署失败"
			if firstErr != "" {
				message += "：" + firstErr
			}
		}
		_, _ = h.db.ExecContext(ctx,
			`UPDATE batch_job_items SET status = ?, message = ?, detail = ?, finished_at = ?
			  WHERE id = ? AND status = 'queued'`,
			status, truncateRunes(message, 500), string(detailJSON), nowMillis(), itemID)
		h.finalizeBatchJobAfterAsync(ctx, jobID)
	}
}

// finalizeBatchJobAfterAsync 所有 item 终态后聚合 batch_jobs 状态。
func (h *handlers) finalizeBatchJobAfterAsync(ctx context.Context, jobID int64) {
	if h.db == nil || jobID <= 0 {
		return
	}
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT COALESCE(status,'') AS status, COUNT(*) AS n FROM batch_job_items WHERE job_id = ? GROUP BY status`, jobID)
	if err != nil {
		return
	}
	var total, successN, failedN, skippedN, activeN int64
	for _, row := range rows {
		n := mapInt(row, "n", 0)
		total += n
		switch mapStr(row, "status", "") {
		case "success":
			successN += n
		case "failed", "timeout":
			failedN += n
		case "skipped":
			skippedN += n
		case "pending", "queued", "running":
			activeN += n
		}
	}
	if activeN > 0 || total == 0 {
		return
	}
	status := "success"
	if failedN > 0 {
		if failedN >= total {
			status = "failed"
		} else {
			status = "partial"
		}
	}
	done := successN + skippedN
	now := nowMillis()
	_, _ = h.db.ExecContext(ctx,
		`UPDATE batch_jobs SET status = ?, done = ?, failed = ?, finished_at = ?, updated_at = ?,
		        locked_at = 0, runner_id = '', dedup_key = NULL
		  WHERE id = ? AND status IN ('waiting','running','queued')`,
		status, done, failedN, now, now, jobID)
	h.recordNodeLog(ctx, 0, 0, "batch-job", map[string]string{"success": "success", "partial": "failed", "failed": "failed"}[status],
		0, "后台任务 #"+strconv.FormatInt(jobID, 10)+" 回执结束："+status, "", "agent")
}

// cronBatchApplyTimeouts 将超过 120s 仍无完整 Agent 回执的批任务项标记 timeout，
// 不能把离线/超时节点算 success。
func (h *handlers) cronBatchApplyTimeouts(ctx context.Context, now int64) {
	if h.db == nil {
		return
	}
	rows, err := queryRowsAsMaps(ctx, h.db,
		`SELECT id, job_id, COALESCE(detail,'') AS detail, COALESCE(started_at,0) AS started_at
		   FROM batch_job_items
		  WHERE status = 'queued' AND target = 'singbox_node' LIMIT 500`)
	if err != nil {
		return
	}
	for _, row := range rows {
		var d batchApplyItemDetail
		_ = json.Unmarshal([]byte(mapStr(row, "detail", "{}")), &d)
		queuedAt := d.QueuedAt
		if queuedAt <= 0 {
			queuedAt = mapInt(row, "started_at", 0)
		}
		if queuedAt <= 0 || now-queuedAt < batchApplyTimeoutMs {
			continue
		}
		d.Timeout = true
		detailJSON, _ := json.Marshal(d)
		res, err := h.db.ExecContext(ctx,
			`UPDATE batch_job_items SET status = 'timeout', message = '等待 Agent 回执超时（120s）', detail = ?, finished_at = ?
			  WHERE id = ? AND status = 'queued'`, string(detailJSON), now, mapInt(row, "id", 0))
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n > 0 {
			h.finalizeBatchJobAfterAsync(ctx, mapInt(row, "job_id", 0))
		}
	}
}
