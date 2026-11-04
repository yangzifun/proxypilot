package api

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 国旗图片代理：flagcdn.com 在部分网络不可达/被墙，改为服务端拉取并缓存。
// GET /api/flag/{cc}

var flagCCRe = regexp.MustCompile(`^[a-z]{2}$`)

type flagCacheEntry struct {
	body []byte
	at   time.Time
}

var flagCache sync.Map // cc -> flagCacheEntry

func (h *handlers) flagImage(w http.ResponseWriter, r *http.Request) {
	cc := strings.ToLower(strings.TrimSpace(pathParam(r, "cc")))
	if !flagCCRe.MatchString(cc) {
		http.NotFound(w, r)
		return
	}
	if v, ok := flagCache.Load(cc); ok {
		e := v.(flagCacheEntry)
		if time.Since(e.at) < 7*24*time.Hour {
			writeFlagBytes(w, e.body)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://flagcdn.com/w40/"+cc+".png", nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil || len(body) == 0 {
		http.NotFound(w, r)
		return
	}
	flagCache.Store(cc, flagCacheEntry{body: body, at: time.Now()})
	writeFlagBytes(w, body)
}

func writeFlagBytes(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	_, _ = w.Write(body)
}
