package main

import (
	"io"
	"log/slog"

	"mirrordock/internal/config"
	"mirrordock/internal/observability"
)

// newLogger creates the service logger from the startup log settings. It
// writes to stdout unless log.path names a file, resolved against configFile;
// the close function releases that file and is a no-op for stdout.
func newLogger(c config.Log, configFile string, stdout io.Writer) (*slog.Logger, func() error, error) {
	return observability.Open(c, configFile, stdout)
}
