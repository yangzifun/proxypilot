// ProxyPilot Go 服务（迁移自 Cloudflare Worker）
//
// 用法：
//
//	PP_MYSQL_DSN='user:pass@tcp(127.0.0.1:3306)/proxypilot?charset=utf8mb4&loc=UTC' \
//	PP_JWT_SECRET='...' ./proxypilot
package main

import (
	"context"
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
var version = "V17.29"

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
	api.EnsureLocalServer(ctx, pool, log)
	api.StartLocalMonitor(ctx, pool, log)
	api.StartCronFor(ctx, cfg, pool, log)
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(cfg, pool, log),
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
