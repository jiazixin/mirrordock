// Command mirrordock syncs and serves package mirrors.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"mirrordock/internal/app"
	"mirrordock/internal/config"
	"mirrordock/internal/syncer"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// shutdownTimeout bounds the graceful shutdown after a signal.
const shutdownTimeout = 30 * time.Second

func main() {
	// rsync starts this internal helper command to reach its daemon through a
	// proxy (RSYNC_CONNECT_PROG); it is not part of the user-facing CLI, and its
	// stdout is the daemon connection, so nothing else may be written there.
	if len(os.Args) > 1 && os.Args[1] == syncer.ConnectCommand {
		os.Exit(runConnect())
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run reports usage and startup errors as plain text on stderr; once the
// logger is ready, failures are logged.
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
	path, explicit, err := o.configPath(os.LookupEnv)
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: config: %v\n", err)
		return exitFailure
	}
	workDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: config: %v\n", err)
		return exitFailure
	}
	cfg, err := config.Open(config.LoadSource{Path: path, Explicit: explicit, WorkDir: workDir, LookupEnv: os.LookupEnv})
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: config: %v\n", err)
		return exitFailure
	}
	logger, closeLog, err := newLogger(cfg.Startup().Log, cfg.File(), stdout)
	if err != nil {
		fmt.Fprintf(stderr, "mirrordock: log: %v\n", err)
		return exitFailure
	}
	defer func() {
		if err := closeLog(); err != nil {
			fmt.Fprintf(stderr, "mirrordock: log: %v\n", err)
		}
	}()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal terminates the process
	logger.Info("service_starting", "version", build.String(), "config", cfg.File())
	application, err := app.OpenConfig(ctx, cfg, logger, build.String())
	if err != nil {
		logger.Error("service_failed", "error", err)
		return exitFailure
	}
	return serve(ctx, logger, application)
}

// serve runs application until ctx ends or serving fails, then shuts it down
// within shutdownTimeout.
func serve(ctx context.Context, logger *slog.Logger, application *app.App) int {
	done := make(chan error, 1)
	var workers sync.WaitGroup
	workers.Go(func() { done <- application.Serve(ctx) })
	defer workers.Wait()
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-done:
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
	}
	if serveErr != nil {
		logger.Error("service_failed", "error", serveErr)
	}
	logger.Info("service_stopping")
	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := application.Close(shutdown); err != nil {
		logger.Error("service_shutdown_failed", "error", err)
		return exitFailure
	}
	if serveErr != nil {
		return exitFailure
	}
	logger.Info("service_stopped")
	return exitOK
}

// runConnect owns the internal relay process and cancels it on termination.
func runConnect() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop)
	return syncer.RunConnect(ctx)
}
