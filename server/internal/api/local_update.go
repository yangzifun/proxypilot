package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
)

// 本机（控制服务器）服务更新：与节点 Agent 任务完全分离。
//
// 背景：本机管理页的 cloudflared「更新」曾调用 /api/jobs（kind=cloudflared.update,
// params={server_ids:[local_id]}），但该 job kind 只认 server_id，参数不匹配时会
// 回退成「全部启用节点」，于是点本机更新实际更新了 15 台节点、本机 cloudflared
// 从未更新（见 batch_job #147）。本文件提供本机专用的异步更新接口：
//   POST /api/local/services/{name}/update  启动更新
//   GET  /api/local/services/{name}/update  查询进度/结果
// 固定脚本、服务名白名单，不接受任何用户输入拼接命令。

// localUpdateResult 是本机服务更新的一次执行结果。
type localUpdateResult struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	Output     string `json:"output,omitempty"`
	Old        string `json:"old,omitempty"`
	New        string `json:"new,omitempty"`
	Active     string `json:"active,omitempty"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
}

var localUpdateState = struct {
	sync.Mutex
	running map[string]bool
	last    map[string]localUpdateResult
}{
	running: map[string]bool{},
	last:    map[string]localUpdateResult{},
}

// localUpdateScripts 是允许在本机执行更新的固定脚本（白名单）。
var localUpdateScripts = map[string]func() string{
	"cloudflared": cloudflaredUpdateCommand,
}

// runLocalUpdateScript 供测试替换。
var runLocalUpdateScript = func(ctx context.Context, service string) (bool, string) {
	gen, ok := localUpdateScripts[service]
	if !ok {
		return false, "不支持的本机服务：" + service
	}
	code, out := runShell(ctx, gen(), 240)
	return code == 0, out
}

// localUpdateService 规范化并校验服务名。
func localUpdateService(raw string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(raw))
	_, ok := localUpdateScripts[name]
	return name, ok
}

// buildLocalUpdateResult 把脚本输出整理成稳定结构（cloudflared 专用摘要）。
func buildLocalUpdateResult(service string, ok bool, out string, started, finished int64) localUpdateResult {
	res := localUpdateResult{
		OK:         ok,
		Output:     tailCloudflaredOutput(out),
		StartedAt:  started,
		FinishedAt: finished,
	}
	if service != "cloudflared" {
		if ok {
			res.Message = "更新完成"
		} else {
			res.Message = "更新失败"
		}
		return res
	}
	already := ""
	active := ""
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "OLD="):
			res.Old = strings.TrimPrefix(t, "OLD=")
		case strings.HasPrefix(t, "NEW="):
			res.New = strings.TrimPrefix(t, "NEW=")
		case strings.HasPrefix(t, "ACTIVE="):
			active = strings.TrimPrefix(t, "ACTIVE=")
		case strings.HasPrefix(t, "ALREADY="):
			already = strings.TrimPrefix(t, "ALREADY=")
		}
	}
	res.Active = active
	switch {
	case already != "":
		res.Old = already
		res.New = already
		res.Message = "已是最新版本：" + already
	case ok:
		if res.Old != "" && res.New != "" && res.Old != res.New {
			res.Message = "已更新：" + res.Old + " → " + res.New
		} else if res.New != "" {
			res.Message = "更新完成：" + res.New
		} else {
			res.Message = "更新完成"
		}
		if active != "" {
			res.Message += "（服务 " + active + "）"
		}
	default:
		res.Message = "更新失败：" + tailCloudflaredOutput(out)
	}
	return res
}

// POST /api/local/services/{name}/update
func (h *handlers) localServiceUpdateStart(w http.ResponseWriter, r *http.Request) {
	service, ok := localUpdateService(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "不支持的本机服务")
		return
	}
	localUpdateState.Lock()
	if localUpdateState.running[service] {
		localUpdateState.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true, "service": service, "started": false, "running": true,
			"message": "正在更新中，请稍候",
		})
		return
	}
	localUpdateState.running[service] = true
	localUpdateState.Unlock()

	actor := "admin"
	if u := currentActor(r); u != "" {
		actor = u
	}
	h.log.Info("本机服务更新开始", "service", service, "actor", actor)

	go func() {
		started := nowMillis()
		ok, out := runLocalUpdateScript(context.Background(), service)
		invalidateLocalVersionCache()
		res := buildLocalUpdateResult(service, ok, out, started, nowMillis())
		localUpdateState.Lock()
		localUpdateState.last[service] = res
		localUpdateState.running[service] = false
		localUpdateState.Unlock()
		h.log.Info("本机服务更新结束", "service", service, "ok", ok, "message", res.Message)
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "service": service, "started": true, "running": true,
		"message": "已开始更新本机 " + service,
	})
}

// GET /api/local/services/{name}/update
func (h *handlers) localServiceUpdateStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := localUpdateService(r.PathValue("name"))
	if !ok {
		writeError(w, http.StatusBadRequest, "不支持的本机服务")
		return
	}
	localUpdateState.Lock()
	running := localUpdateState.running[service]
	last, hasLast := localUpdateState.last[service]
	localUpdateState.Unlock()

	resp := map[string]any{
		"success": true,
		"service": service,
		"running": running,
	}
	if hasLast {
		resp["last"] = last
	}
	writeJSON(w, http.StatusOK, resp)
}

// currentActor 返回当前请求的操作者（管理端审计用户名），取不到时返回空串。
func currentActor(r *http.Request) string {
	if r == nil {
		return ""
	}
	if claims, ok := claimsFrom(r); ok && strings.TrimSpace(claims.Sub) != "" {
		return claims.Sub
	}
	return ""
}
