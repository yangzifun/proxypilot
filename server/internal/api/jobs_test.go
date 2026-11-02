package api

import "testing"

func TestBatchJobJSON(t *testing.T) {
	row := map[string]any{
		"id": int64(7), "kind": "singbox.apply_all", "title": "一键下发",
		"params": `{"force":true,"push":false}`, "status": "success",
		"total": int64(3), "done": int64(3), "failed": int64(0), "cursor": int64(3),
		"actor": "admin", "created_at": int64(100), "updated_at": int64(200),
	}
	j := batchJobToJSON(row)
	if j.ID != 7 || j.Kind != "singbox.apply_all" || j.Total != 3 || j.Done != 3 {
		t.Fatalf("job = %#v", j)
	}
	params, ok := j.Params.(map[string]any)
	if !ok || params["force"] != true {
		t.Fatalf("params = %#v", j.Params)
	}
}

func TestBatchItemJSON(t *testing.T) {
	row := map[string]any{
		"id": int64(1), "seq": int64(2), "target": "singbox_node", "target_id": int64(9),
		"label": "jp-01", "status": "failed", "message": "boom",
		"detail": `{"changed":1}`, "attempts": int64(2),
	}
	item := batchItemToJSON(row)
	if item["seq"].(int64) != 2 || item["target_id"].(int64) != 9 {
		t.Fatalf("item = %#v", item)
	}
	detail, ok := item["detail"].(map[string]any)
	if !ok || detail["changed"].(float64) != 1 {
		t.Fatalf("detail = %#v", item["detail"])
	}
}
