package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/yzfn/proxypilot/masque/node"
)

// Version 由构建脚本注入。
var Version = "1.0.1"

func main() {
	cfgPath := flag.String("config", "/etc/proxypilot/masque/config.json", "节点配置文件")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, err := node.LoadConfig(*cfgPath)
	if err != nil {
		log.Error("加载配置失败", "err", err, "path", *cfgPath)
		os.Exit(1)
	}

	srv, err := node.New(cfg, log)
	if err != nil {
		log.Error("初始化节点失败", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("pp-masque-node 启动", "version", Version, "config", *cfgPath)
	if err := srv.Run(ctx); err != nil {
		log.Error("节点退出", "err", err)
		os.Exit(1)
	}
}



