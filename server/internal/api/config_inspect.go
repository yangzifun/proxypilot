package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// inspectConfigFileJSON 只读检查本机配置文件，返回与 Agent inspect 动作一致的 JSON：
// {"path":..., "sha256":..., "size":..., "users":[...], "inbounds":N, "error":"..."}
func inspectConfigFileJSON(path string) string {
	res := map[string]any{"path": path}
	if strings.TrimSpace(path) == "" {
		res["error"] = "路径为空"
		b, _ := json.Marshal(res)
		return string(b)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		res["error"] = err.Error()
		b, _ := json.Marshal(res)
		return string(b)
	}
	sum := sha256.Sum256(data)
	res["sha256"] = hex.EncodeToString(sum[:])
	res["size"] = len(data)
	var cfg struct {
		Inbounds []struct {
			Users []struct {
				Name     string `json:"name"`
				UUID     string `json:"uuid"`
				Password string `json:"password"`
			} `json:"users"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		res["inbounds"] = 0
		res["error"] = "不是合法 JSON：" + err.Error()
		b, _ := json.Marshal(res)
		return string(b)
	}
	seen := map[string]bool{}
	users := make([]string, 0, 16)
	for _, ib := range cfg.Inbounds {
		for _, u := range ib.Users {
			n := strings.TrimSpace(u.Name)
			if n == "" {
				n = strings.TrimSpace(u.UUID)
			}
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			users = append(users, n)
		}
	}
	res["users"] = users
	res["inbounds"] = len(cfg.Inbounds)
	b, _ := json.Marshal(res)
	return string(b)
}

// nodeConfigProbe 一个节点的机器实际配置探测结果。
type nodeConfigProbe struct {
	NodeID   int64
	ServerID int64
	Name     string
	Path     string
	TaskID   int64
	Users    []string
	Sha256   string
	Err      string
}

// probeNodeConfigs 让每台节点上的 Agent 读取真实 sb.json（只读），并等待回执。
// 只对远程节点排队 inspect 任务；本机节点直接读文件。最多等 100 秒。
func (h *handlers) probeNodeConfigs(ctx context.Context, rows []map[string]any) map[int64]*nodeConfigProbe {
	probes := map[int64]*nodeConfigProbe{}
	pending := map[int64]*nodeConfigProbe{}
	for _, row := range rows {
		id := mapInt(row, "id", 0)
		if id == 0 {
			continue
		}
		path := safeInspectPath(strings.TrimSpace(mapStr(row, "target_path", "")))
		p := &nodeConfigProbe{NodeID: id, ServerID: mapInt(row, "server_id", 0),
			Name: mapStr(row, "name", ""), Path: path}
		probes[id] = p
	}
	for _, p := range probes {
		srv, err := h.fetchAgentServer(ctx, p.ServerID)
		if err != nil {
			p.Err = "服务器不存在"
			continue
		}
		if strings.EqualFold(srv.LinkMode, "local") {
			out := inspectConfigFileJSON(p.Path)
			fillProbeFromJSON(p, out)
			continue
		}
		taskID, _, msg := h.agentQueueTask(ctx, srv, "inspect", 0,
			map[string]any{"path": p.Path, "timeout": 20}, "uuid-reconcile")
		if msg != "" || taskID == 0 {
			p.Err = firstNonEmpty(msg, "排队失败")
			continue
		}
		p.TaskID = taskID
		pending[taskID] = p
	}
	deadline := time.Now().Add(100 * time.Second)
	for len(pending) > 0 && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return probes
		case <-time.After(2 * time.Second):
		}
		ids := make([]string, 0, len(pending))
		for id := range pending {
			ids = append(ids, itoa64(id))
		}
		rows, err := queryRowsAsMaps(ctx, h.db,
			`SELECT id, status, COALESCE(output,'') AS output FROM agent_tasks WHERE id IN (`+strings.Join(ids, ",")+`)`)
		if err != nil {
			continue
		}
		for _, r := range rows {
			status := mapStr(r, "status", "")
			if status != "done" && status != "failed" && status != "skipped" {
				continue
			}
			tid := mapInt(r, "id", 0)
			p := pending[tid]
			if p == nil {
				continue
			}
			delete(pending, tid)
			if status != "done" {
				p.Err = firstNonEmpty(strings.TrimSpace(mapStr(r, "output", "")), "任务未完成")
				continue
			}
			fillProbeFromJSON(p, mapStr(r, "output", ""))
		}
	}
	for _, p := range pending {
		p.Err = "等待 Agent 回执超时"
	}
	return probes
}

func fillProbeFromJSON(p *nodeConfigProbe, raw string) {
	var out struct {
		Users  []string `json:"users"`
		Sha256 string   `json:"sha256"`
		Error  string   `json:"error"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		p.Err = "回执解析失败"
		return
	}
	if out.Error != "" {
		p.Err = out.Error
		return
	}
	p.Users = out.Users
	p.Sha256 = out.Sha256
}

// safeInspectPath 仅允许配置目录内的普通 JSON 文件。
func safeInspectPath(p string) string {
	def := "/etc/sing-box/sb.json"
	if strings.TrimSpace(p) == "" {
		return def
	}
	abs, err := filepath.Abs(filepath.Clean(p))
	if err != nil {
		return def
	}
	for _, root := range []string{"/etc/sing-box/", "/etc/mihomo/", "/etc/mtg/"} {
		if strings.HasPrefix(abs, root) {
			return abs
		}
	}
	return def
}
