// ProxyPilot Go 服务（迁移自 Cloudflare Worker）
//
// 用法：
//
//	PP_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/proxypilot?charset=utf8mb4&loc=UTC' \
//	PP_JWT_SECRET='...' ./proxypilot
package main

import (
	"context"
	"strings"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yzfn/proxypilot/server/internal/api"
	"github.com/yzfn/proxypilot/server/internal/config"
	"github.com/yzfn/proxypilot/server/internal/db"
	"github.com/yzfn/proxypilot/server/migrations"
)

// version 由构建脚本注入：-ldflags "-X main.version=v1.0.0"
var version = "V1.2"

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("服务退出", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.AutoMigrate {
		applied, err := db.Migrate(ctx, pool, migrations.FS)
		if err != nil {
			return err
		}
		if len(applied) > 0 {
			log.Info("数据库迁移完成", slog.Any("applied", applied))
		}
	}

	api.Version = version
	api.EnsureAdminPassword(ctx, pool, cfg, log)
	// 密钥持久化：JWT / Agent 密钥写入 app_settings，重启或容器重建后登录态与 Agent 令牌不失效
	cfg.JWTSecret = api.EnsurePersistentSecret(ctx, pool, "jwt_secret", cfg.JWTSecret, 32, log)
	cfg.AgentKeySecret = api.EnsurePersistentSecret(ctx, pool, "agent_key_secret", cfg.AgentKeySecret, 16, log)
	api.ReloadCDNPreferred(ctx, pool)
	if len(cfg.JWTSecret) < 32 && !cfg.AllowDevSecret {
		return errors.New("JWT 密钥不足 32 字节：请设置 PP_JWT_SECRET，或确认 app_settings.jwt_secret 存在；开发环境可设 PP_DEV=1")
	}
	api.EnsureLocalServer(ctx, pool, log)
	api.StartLocalMonitor(ctx, pool, log)
	api.StartCronFor(ctx, cfg, pool, log)
	api.StartConfigSelfHeal(ctx, cfg, pool, log)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           securityHeaders(api.New(cfg, pool, log)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务已启动", "addr", cfg.Addr, "version", version)
		errCh <- srv.ListenAndServe()
	}()

	// Agent 内网监听：绑定 WARP 内网 IP，明文（隧道已加密），仅 Agent 接口
	// 支持多个内网监听地址（逗号分隔），便于切换端口时保留旧入口
	for _, addr := range strings.Split(cfg.AgentInternalAddr, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		innerSrv := &http.Server{
			Addr:              addr,
			Handler:           agentOnlyHandler(api.New(cfg, pool, log)),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       60 * time.Second,
			WriteTimeout:      180 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func(a string, s *http.Server) {
			log.Info("Agent 内网监听已启动", "addr", a)
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("Agent 内网监听失败", "addr", a, "err", err)
			}
		}(addr, innerSrv)
	}

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info("收到退出信号，开始优雅关闭")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// securityHeaders 统一追加基础安全响应头。
//
// CSP 由 api 包统一设置（强制模式、常量策略，不依赖每请求 nonce），见
// api/middleware_security.go；这里不再写 Content-Security-Policy-Report-Only，
// 避免「只报告不拦截」。
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// agentOnlyHandler 限制直连端口只服务 Agent 相关接口，避免网页暴露在源站 IP。
func agentOnlyHandler(next http.Handler) http.Handler {
	allow := func(p string) bool {
		switch {
		case p == "/healthz", p == "/api/healthz":
			return true
		case strings.HasPrefix(p, "/api/agent/"):
			return true
		case strings.HasPrefix(p, "/agent-dist/"):
			return true
		case strings.HasPrefix(p, "/api/shell/agent/"):
			// Agent 拨号建立实时终端隧道（仅 Agent 令牌可用）
			return true
		case p == "/install.sh", p == "/agent/install.sh":
			return true
		}
		return false
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allow(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}


























