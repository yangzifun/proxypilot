package api

import "testing"

func TestBatchKindsComplete(t *testing.T) {
	want := map[string]bool{
		"singbox.apply_all": true, "singbox.push": true,
		"mtproto.apply_all": true, "mtproto.template_apply": true,
		"agent.update": true, "ssh.toggle": true, "uuid.reconcile": true, "cloudflared.update": true,
		// warp.update 在 V17 之后补上的（jobs_kinds.go），用例当时没同步，
		// 导致 batchKinds=9 而 want=8 长期失败。这里补上并说明用途。
		"warp.update": true,
	}
	if len(batchKinds) != len(want) {
		t.Fatalf("batchKinds = %d, want %d", len(batchKinds), len(want))
	}
	for k := range want {
		def, ok := batchKinds[k]
		if !ok || def.Items == nil || def.Run == nil {
			t.Fatalf("缺少任务类型 %s", k)
		}
	}
}
