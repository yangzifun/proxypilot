package api

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// verifySem 限制同时进行的授权后一致性校验数量。
var verifySem = make(chan struct{}, 4)

// verifyNodeConsistencyAsync 在授权/解除授权后异步核对「机器实际配置 + uuid_list + configs」，
// 发现不一致立即修复（重渲染、重建订阅配置、排队下发）。
func (h *handlers) verifyNodeConsistencyAsync(nodeID int64) {
	if nodeID <= 0 {
		return
	}
	go func() {
		select {
		case verifySem <- struct{}{}:
			defer func() { <-verifySem }()
		case <-time.After(5 * time.Second):
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		h.verifyNodeConsistency(ctx, nodeID)
	}()
}

// verifyNodeConsistency 单节点三向一致性校验 + 修复。
func (h *handlers) verifyNodeConsistency(ctx context.Context, nodeID int64) {
	rows, err := queryRowsAsMaps(ctx, h.db, `SELECT n.id AS id, n.server_id AS server_id,
	      COALESCE(n.name,'') AS name, COALESCE(n.uuid_list,'[]') AS uuid_list,
	      COALESCE((SELECT c.target_path FROM node_configs c WHERE c.name = CONCAT('sbx:', n.name, ':sb') ORDER BY c.id DESC LIMIT 1), '') AS target_path
	    FROM singbox_nodes n WHERE n.id = ? LIMIT 1`, nodeID)
	if err != nil || len(rows) == 0 {
		return
	}
	var expected []string
	_ = json.Unmarshal([]byte(mapStr(rows[0], "uuid_list", "[]")), &expected)
	expectedLower := map[string]bool{}
	for _, u := range expected {
		if s := strings.ToLower(strings.TrimSpace(u)); s != "" {
			expectedLower[s] = true
		}
	}

	// 机器侧（只读探测）
	probes := h.probeNodeConfigs(ctx, rows)
	drift := false
	if p := probes[nodeID]; p != nil {
		if len(p.Users) > 0 {
			onNode := map[string]bool{}
			for _, u := range p.Users {
				if s := strings.ToLower(strings.TrimSpace(u)); s != "" {
					onNode[s] = true
				}
			}
			for id := range onNode {
				if !expectedLower[id] {
					drift = true
				}
			}
			for id := range expectedLower {
				if !onNode[id] {
					drift = true
				}
			}
		}
	}

	// 订阅配置侧
	if crow, err := h.db.QueryContext(ctx, `SELECT DISTINCT uuid FROM configs WHERE singbox_node_id = ?`, nodeID); err == nil {
		have := map[string]bool{}
		for crow.Next() {
			var u string
			if crow.Scan(&u) == nil {
				have[strings.ToLower(strings.TrimSpace(u))] = true
			}
		}
		_ = crow.Close()
		for id := range expectedLower {
			if !have[id] {
				drift = true
			}
		}
		for id := range have {
			if !expectedLower[id] {
				drift = true
			}
		}
	}
	if !drift {
		return
	}

	want := h.countNodeConfigsFor(ctx, nodeID)
	tpl, node, _, _, msg := h.loadSingboxNodeFull(ctx, nodeID)
	if msg != "" {
		return
	}
	users := singboxUsersFromUUIDs(tpl, expected)
	files, _, _, _, msg := h.applySingboxNode(ctx, nodeID, users, false)
	if msg != "" {
		return
	}
	if _, _, err := h.syncSingboxConfigsForUsers(ctx, nodeID, node.Name, tpl, node, users); err != nil {
		return
	}
	h.queueSingboxDeploy(ctx, files, "auth-verify")
	machineUsers := 0
	if p := probes[nodeID]; p != nil {
		machineUsers = len(p.Users)
	}
	h.recordNodeLog(ctx, mapInt(rows[0], "server_id", 0), 0, "uuid-verify", "success", 0,
		"授权后一致性校验发现漂移并修复：授权 "+itoa64(int64(len(expected)))+" 个，原订阅配置 "+itoa64(want)+
			" 个，机器侧 "+itoa64(int64(machineUsers))+" 个", "", "auth-verify")
}

func (h *handlers) countNodeConfigsFor(ctx context.Context, nodeID int64) int64 {
	var n int64
	_ = h.db.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT uuid) FROM configs WHERE singbox_node_id = ?`, nodeID).Scan(&n)
	return n
}
