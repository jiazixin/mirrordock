package main

import (
	"context"
	"errors"
	"fmt"
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
	options, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if options.Help {
		fmt.Print(usageText)
		return 0
	}
	if options.Version {
		fmt.Println("mirrordock", versionString())
		return 0
	}
	cfg, err := config.Open(options.ConfigPath)
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
	application.Version = versionString()
	slog.Info("MirrorDock 启动中",
		"version", versionString(),
		"address", application.Server.Addr,
		"admin", "/admin/",
		"config", application.Config.File(),
	)
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
