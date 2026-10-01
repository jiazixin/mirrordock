package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"mirrordock/internal/app"
	"mirrordock/internal/config"
	"mirrordock/internal/observability"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	os.Exit(run())
}

func run() int {
	var short, long string
	flag.StringVar(&short, "f", "", "配置文件路径")
	flag.StringVar(&long, "config", "", "配置文件路径")
	flag.Parse()
	invalidEmpty := false
	flag.Visit(func(f *flag.Flag) {
		if (f.Name == "f" || f.Name == "config") && f.Value.String() == "" {
			invalidEmpty = true
		}
	})
	if invalidEmpty {
		slog.Error("配置文件路径不能留空")
		return 1
	}
	file := "config.yaml"
	if short != "" {
		file = short
	}
	if long != "" {
		if short != "" && short != long {
			slog.Error("-f 和 --config 不一致")
			return 1
		}
		file = long
	}
	if flag.NArg() > 0 {
		slog.Error("不接受额外位置参数")
		return 1
	}
	cfg, err := config.Open(file)
	if err != nil {
		slog.Error("启动失败", "error", err)
		return 1
	}
	logger, closeLog, err := observability.Open(cfg.Startup().Log, cfg.File())
	if err != nil {
		slog.Error("启动失败", "error", err)
		return 1
	}
	slog.SetDefault(logger)
	defer func() {
		if err := closeLog(); err != nil {
			fallback := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			fallback.Error("关闭日志异常", "error", err)
		}
	}()
	application, err := app.OpenConfig(cfg)
	if err != nil {
		slog.Error("启动失败", "error", err)
		return 1
	}
	slog.Info("MirrorDock 已启动", "address", application.Server.Addr, "admin", "/admin/", "config", application.Config.File())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- application.Serve() }()
	select {
	case <-ctx.Done():
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP 服务退出", "error", err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if closeErr := application.Close(shutdown); closeErr != nil {
		slog.Error("关闭异常", "error", closeErr)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 1
	}
	return 0
}
