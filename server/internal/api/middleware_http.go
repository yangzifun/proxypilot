package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

// cors 只对「同源请求」返回 CORS 头（低-06）。
//
// 之前无条件返回 Access-Control-Allow-Origin: *；虽然 Bearer 认证下跨站读不到
// 数据，但没有任何合法跨域调用需要它，* 只会放宽纵深防御。当前前端与面板同源，
// 因此只反射与本站 Host/配置基址一致的 Origin，其它 Origin 不返回 CORS 头。
func (h *handlers) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin != "" && sameOriginRequest(r, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginRequest 判断 Origin 是否与本站同源（Host 或配置的对外基址）。
func sameOriginRequest(r *http.Request, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	if base := configuredSiteOrigin(); base != "" {
		if b, err := url.Parse(base); err == nil && strings.EqualFold(u.Host, b.Host) {
			return true
		}
	}
	return false
}

func (h *handlers) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		h.log.Info("http",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Duration("took", time.Since(start)),
		)
	})
}

func (h *handlers) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				h.log.Error("panic", slog.Any("value", v), slog.String("stack", string(debug.Stack())))
				writeError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
