package observability

import (
	"fmt"
	"io"
	"log/slog"
	"mirrordock/internal/config"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Open returns a logger and a close function; stdout remains owned by the process.
func Open(c config.Log, file string) (*slog.Logger, func() error, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Level)); err != nil {
		return nil, nil, fmt.Errorf("log.level: %w", err)
	}
	if c.Format != "json" && c.Format != "text" {
		return nil, nil, fmt.Errorf("log.format 只支持 json、text")
	}
	var output io.Writer = os.Stdout
	closeLog := func() error { return nil }
	if c.Path != "" {
		p := config.Resolve(file, c.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			return nil, nil, fmt.Errorf("创建日志目录: %w", err)
		}
		// A FIFO must fail without waiting for a reader during startup.
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND|unix.O_NONBLOCK, 0600)
		if err != nil {
			return nil, nil, fmt.Errorf("打开日志文件: %w", err)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = f.Close()
			return nil, nil, fmt.Errorf("log.path 必须是普通文件")
		}
		if err := f.Chmod(0600); err != nil {
			_ = f.Close()
			return nil, nil, fmt.Errorf("设置日志文件权限: %w", err)
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
