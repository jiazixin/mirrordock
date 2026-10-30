package app

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"mirrordock/internal/config"
)

// openFile loads a configuration file and opens the application on it, as
// main does with its parsed flags.
func openFile(ctx context.Context, file string) (*App, error) {
	cfg, err := config.Open(config.LoadSource{Path: file, Explicit: true})
	if err != nil {
		return nil, err
	}
	return OpenConfig(ctx, cfg, slog.New(slog.DiscardHandler), "dev")
}

// reloadConfig rereads the YAML file without the runtime change checks.
func reloadConfig(m *config.Manager) error {
	return m.ReloadChecked(func(config.Config) error { return nil })
}

// closeTestApp remains callable from defer and t.Cleanup after test cancellation.
func closeTestApp(t *testing.T, application *App) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
	defer cancel()
	return application.Close(ctx)
}
