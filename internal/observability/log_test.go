package observability

import (
	"encoding/json/v2"
	"log/slog"
	"mirrordock/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestFileLoggingFormatLevelAndAppend(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "config.yaml")
			c := config.Log{Level: "warn", Path: "logs/server.log", Format: format}
			for range 2 {
				logger, closeLog, err := Open(c, file)
				if err != nil {
					t.Fatal(err)
				}
				logger.Info("filtered")
				logger.Warn("retained", "number", 42)
				if err := closeLog(); err != nil {
					t.Fatal(err)
				}
			}
			p := filepath.Join(dir, c.Path)
			b, err := os.ReadFile(p)
			if err != nil || strings.Contains(string(b), "filtered") || strings.Count(string(b), "retained") != 2 {
				t.Fatalf("unexpected output %q: %v", b, err)
			}
			for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
				if format == "json" {
					var record map[string]any
					if err := json.Unmarshal([]byte(line), &record); err != nil || record["level"] != "WARN" || record["msg"] != "retained" || record["number"] != float64(42) {
						t.Fatalf("invalid JSON record: %s, %v", line, err)
					}
				} else if !strings.Contains(line, "level=WARN msg=retained number=42") {
					t.Fatalf("invalid text record: %s", line)
				}
			}
			info, err := os.Stat(p)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("unexpected file permissions", err)
			}
		})
	}
}

func TestLevelsAndOpenErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	for _, level := range []string{"debug", "info", "warn", "error"} {
		logger, closeLog, err := Open(config.Log{Level: level, Format: "json"}, file)
		if err != nil {
			t.Fatal(err)
		}
		if logger.Enabled(t.Context(), slog.LevelDebug) != (level == "debug") || !logger.Enabled(t.Context(), slog.LevelError) {
			t.Fatal("wrong level filter", level)
		}
		if err := closeLog(); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []config.Log{{Level: "invalid", Format: "json"}, {Level: "info", Format: "xml"}, {Level: "info", Format: "json", Path: filepath.Dir(file)}} {
		if _, _, err := Open(c, file); err == nil {
			t.Fatal("invalid logger accepted")
		}
	}
	fifo := filepath.Join(filepath.Dir(file), "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Open(config.Log{Level: "info", Format: "json", Path: fifo}, file); err == nil {
		t.Fatal("FIFO accepted as log file")
	}
}
