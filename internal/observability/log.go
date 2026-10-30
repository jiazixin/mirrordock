package observability

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"mirrordock/internal/config"
)

// Open returns a logger and a close function. The logger writes to stdout
// unless c.Path names a log file, resolved against the configuration file;
// stdout remains owned by the caller and is never closed.
func Open(c config.Log, file string, stdout io.Writer) (*slog.Logger, func() error, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		return nil, nil, fmt.Errorf("log.level: %w", err)
	}
	if c.Format != "json" && c.Format != "text" {
		return nil, nil, errors.New("log.format: must be json or text")
	}
	output := stdout
	closeLog := func() error { return nil }
	if c.Path != "" {
		p := config.Resolve(file, c.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return nil, nil, fmt.Errorf("create log directory: %w", err)
		}
		// A FIFO must fail without waiting for a reader during startup.
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND|unix.O_NONBLOCK, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("open log file: %w", err)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return nil, nil, errors.Join(errors.New("log.path: must be a regular file"), err, f.Close())
		}
		if err := f.Chmod(0o600); err != nil {
			return nil, nil, errors.Join(fmt.Errorf("set log file permissions: %w", err), f.Close())
		}
		output, closeLog = f, f.Close
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewJSONHandler(output, opts)
	if c.Format == "text" {
		handler = slog.NewTextHandler(output, opts)
	}
	return slog.New(handler), closeLog, nil
}
