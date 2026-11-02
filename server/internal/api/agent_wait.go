package api

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// waitHub 用于 /api/agent/wait 长轮询：任务入队 / 终端会话创建时提前唤醒。
var waitHub = struct {
	sync.Mutex
	chans map[int64]map[chan struct{}]struct{}
}{chans: map[int64]map[chan struct{}]struct{}{}}

func wakeWaiters(serverID int64) {
	waitHub.Lock()
	set := waitHub.chans[serverID]
	delete(waitHub.chans, serverID)
	waitHub.Unlock()
	for ch := range set {
		close(ch)
	}
}

func registerWaiter(serverID int64) (chan struct{}, func()) {
	ch := make(chan struct{})
	waitHub.Lock()
	if waitHub.chans[serverID] == nil {
		waitHub.chans[serverID] = map[chan struct{}]struct{}{}
	}
	waitHub.chans[serverID][ch] = struct{}{}
	waitHub.Unlock()
	return ch, func() {
		waitHub.Lock()
		if set := waitHub.chans[serverID]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(waitHub.chans, serverID)
			}
		}
		waitHub.Unlock()
	}
}

func (h *handlers) hasPendingWork(ctx context.Context, serverID int64) bool {
	var n int64
	if err := h.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE server_id = ? AND status = 'pending'`, serverID).Scan(&n); err == nil && n > 0 {
		return true
	}
	if _, ok := h.pendingShellSession(ctx, serverID); ok {
		return true
	}
	return false
}

// GET/POST /api/agent/wait —— 真实长轮询（无 Durable Object）：
// 200=有任务/终端待处理，204=超时，401=鉴权失败。
func (h *handlers) agentWait(w http.ResponseWriter, r *http.Request) {
	srv, status, msg := h.agentAuthenticate(r, nil)
	if srv == nil {
		writeError(w, status, msg)
		return
	}
	timeout := intOrJS(r.URL.Query().Get("timeout"), 20, 5, 25)
	if h.hasPendingWork(r.Context(), srv.ID) {
		w.WriteHeader(http.StatusOK)
		return
	}
	ch, cancel := registerWaiter(srv.ID)
	defer cancel()
	timer := time.NewTimer(time.Duration(timeout) * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			w.WriteHeader(http.StatusOK)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-tick.C:
			if h.hasPendingWork(r.Context(), srv.ID) {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}
}
