package api

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"
)

func TestRecordBatchApplyTaskResultSuccess(t *testing.T) {
	detail := `{"node_id":1,"task_ids":[11,12],"task_total":2,"queued_at":1000}`
	var itemStatus string
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "FROM batch_job_items") && strings.Contains(q, "status = 'queued'"):
			return fakeRowsSet([]string{"id", "job_id", "detail"},
				[]driver.Value{int64(5), int64(7), detail}), nil
		case strings.Contains(q, "GROUP BY status"):
			return fakeRowsSet([]string{"status", "n"},
				[]driver.Value{"success", int64(2)}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE batch_job_items SET detail") {
			detail, _ = args[0].(string)
		}
		if strings.Contains(q, "UPDATE batch_job_items SET status") {
			if v, ok := args[0].(string); ok {
				itemStatus = v
			}
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.recordBatchApplyTaskResult(context.Background(), 11, true, "")
	if itemStatus != "" {
		t.Fatal("多任务全部回执前不应结束 item")
	}
	h.recordBatchApplyTaskResult(context.Background(), 12, true, "")
	if itemStatus != "success" {
		t.Fatalf("全部成功应 item success，实际 %q", itemStatus)
	}
	var jobFinal string
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "UPDATE batch_jobs SET status = ?") && len(c.Args) > 0 {
			jobFinal = c.Args[0].(string)
		}
	}
	if jobFinal != "success" {
		t.Fatalf("job 应 success，实际 %q", jobFinal)
	}
}

func TestRecordBatchApplyTaskResultFailurePartial(t *testing.T) {
	detail := `{"node_id":1,"task_ids":[21,22],"task_total":2,"queued_at":1000}`
	var itemStatus string
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM batch_job_items") && strings.Contains(q, "status = 'queued'") {
			return fakeRowsSet([]string{"id", "job_id", "detail"},
				[]driver.Value{int64(6), int64(8), detail}), nil
		}
		if strings.Contains(q, "GROUP BY status") {
			return fakeRowsSet([]string{"status", "n"},
				[]driver.Value{"success", int64(1)},
				[]driver.Value{"failed", int64(1)},
			), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE batch_job_items SET detail") {
			detail, _ = args[0].(string)
		}
		if strings.Contains(q, "UPDATE batch_job_items SET status") && len(args) > 0 {
			itemStatus, _ = args[0].(string)
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.recordBatchApplyTaskResult(context.Background(), 21, true, "")
	h.recordBatchApplyTaskResult(context.Background(), 22, false, "sing-box check failed")
	if itemStatus != "failed" {
		t.Fatalf("部分失败应 item failed，实际 %q", itemStatus)
	}
	var jobFinal string
	for _, c := range fdb.execCalls() {
		if strings.Contains(c.Query, "UPDATE batch_jobs SET status = ?") && len(c.Args) > 0 {
			jobFinal = c.Args[0].(string)
		}
	}
	if jobFinal != "partial" {
		t.Fatalf("partial 任务应 job partial，实际 %q", jobFinal)
	}
	if !strings.Contains(detail, "task_results") {
		t.Fatalf("item detail 缺少 task_results：%s", detail)
	}
}

func TestCronBatchApplyTimeout(t *testing.T) {
	detail := `{"node_id":3,"task_ids":[31],"task_total":1,"queued_at":100}`
	var itemStatus string
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "FROM batch_job_items") && strings.Contains(q, "status = 'queued'") {
			return fakeRowsSet([]string{"id", "job_id", "detail", "started_at"},
				[]driver.Value{int64(9), int64(10), detail, int64(100)}), nil
		}
		if strings.Contains(q, "GROUP BY status") {
			return fakeRowsSet([]string{"status", "n"},
				[]driver.Value{"timeout", int64(1)}), nil
		}
		return &fakeRows{}, nil
	}
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE batch_job_items SET status = 'timeout'") {
			itemStatus = "timeout"
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.cronBatchApplyTimeouts(context.Background(), 100+batchApplyTimeoutMs+1)
	if itemStatus != "timeout" {
		t.Fatal("超时 item 应标记 timeout")
	}
}

func TestFinalizeBatchJobPartial(t *testing.T) {
	fdb, db := newFakeDB(t)
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		if strings.Contains(q, "GROUP BY status") {
			return fakeRowsSet([]string{"status", "n"},
				[]driver.Value{"success", int64(2)},
				[]driver.Value{"failed", int64(1)},
			), nil
		}
		return &fakeRows{}, nil
	}
	var final string
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE batch_jobs SET status = ?") && len(args) > 0 {
			final = args[0].(string)
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.finalizeBatchJobAfterAsync(context.Background(), 1)
	if final != "partial" {
		t.Fatalf("2 成功 1 失败应为 partial，实际 %q", final)
	}
	_ = json.Valid([]byte(`{}`))
}

func TestRunBatchJobAsyncLeavesWaiting(t *testing.T) {
	const kind = "test.async_batch"
	old, existed := batchKinds[kind]
	t.Cleanup(func() {
		if existed {
			batchKinds[kind] = old
		} else {
			delete(batchKinds, kind)
		}
	})
	batchKinds[kind] = jobKind{
		Title: func(_ map[string]any, n int) string { return "async" },
		Items: func(h *handlers, ctx context.Context, _ map[string]any) ([]jobItem, error) {
			return []jobItem{{Target: "singbox_node", TargetID: 1, Label: "n1"}}, nil
		},
		RunAsync: func(h *handlers, ctx context.Context, _ map[string]any, item jobItem, _ string) (string, string, map[string]any) {
			return "queued", "queued", map[string]any{"task_ids": []int64{101}, "queued_at": nowMillis()}
		},
	}

	fdb, db := newFakeDB(t)
	runner := ""
	fdb.onQuery = func(q string, args []driver.Value) (driver.Rows, error) {
		switch {
		case strings.Contains(q, "SELECT * FROM batch_jobs WHERE id = ?"):
			return fakeRowSet([]string{"id", "kind", "params", "status", "runner_id"},
				map[string]any{"id": int64(1), "kind": kind, "params": "{}", "status": "queued", "runner_id": ""}), nil
		case strings.Contains(q, "SELECT COALESCE(status,''), COALESCE(runner_id,'') FROM batch_jobs"):
			return fakeRowsSet([]string{"status", "runner_id"}, []driver.Value{"running", runner}), nil
		case strings.Contains(q, "SELECT * FROM batch_job_items WHERE job_id = ? AND status = 'pending'"):
			return fakeRowsSet([]string{"id", "target", "target_id", "label", "status"},
				[]driver.Value{int64(5), "singbox_node", int64(1), "n1", "pending"}), nil
		}
		return &fakeRows{}, nil
	}
	var gotQueued, gotWaiting bool
	fdb.onExec = func(q string, args []driver.Value) (driver.Result, error) {
		if strings.Contains(q, "UPDATE batch_jobs SET status = 'running'") && len(args) > 1 {
			runner, _ = args[1].(string)
		}
		if strings.Contains(q, "UPDATE batch_job_items SET status = 'queued'") {
			gotQueued = true
		}
		if strings.Contains(q, "UPDATE batch_jobs SET status = 'waiting'") {
			gotWaiting = true
		}
		return fakeResult(1, 0), nil
	}
	h := &handlers{db: db, log: testLogger()}
	h.runBatchJobInline(context.Background(), 1, "tester")
	if !gotQueued || !gotWaiting {
		t.Fatalf("异步任务应 queued/waiting：queued=%v waiting=%v", gotQueued, gotWaiting)
	}
}
