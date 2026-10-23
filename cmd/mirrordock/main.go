// Command mirrordock syncs and serves package mirrors.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mirrordock/internal/app"
	"mirrordock/internal/config"
	"mirrordock/internal/observability"
	syncer "mirrordock/internal/sync"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

func main() {
	// rsync starts this internal command to reach its daemon through a proxy;
	// stdout is the daemon connection, so nothing else may be written there.
	if len(os.Args) > 1 && os.Args[1] == syncer.ConnectCommand {
		os.Exit(syncer.RunConnect())
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run reports usage and startup errors as plain text on stderr; once the
// configuration loads, the service logs through its configured logger.
func run(args []string, stdout, stderr io.Writer) int {
	o, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: %v\nRun 'mirrordock --help' for usage.\n", err)
		return exitUsage
	}
	build := currentBuild()
	switch {
	case o.help:
		fmt.Fprint(stdout, usage)
		return exitOK
	case o.version:
		fmt.Fprintln(stdout, "mirrordock", build)
		return exitOK
	}
	path, err := o.configPath(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: config: %v\n", err)
		return exitFailure
	}
	cfg, err := config.Open(path)
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: config: %v\n", err)
		return exitFailure
	}
	logger, closeLog, err := observability.Open(cfg.Startup().Log, cfg.File())
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: log: %v\n", err)
		return exitFailure
	}
	slog.SetDefault(logger)
	defer func() {
		if err := closeLog(); err != nil {
			fallback := slog.New(slog.NewJSONHandler(stderr, nil))
			fallback.Error("关闭日志异常", "error", err)
		}
	}()
	application, err := app.OpenConfig(cfg)
	if err != nil {
		slog.Error("启动失败", "error", err)
		return exitFailure
	}
	application.Version = build.String()
	slog.Info("MirrorDock 启动中",
		"version", application.Version,
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
		return exitFailure
	}
	return exitOK
}
