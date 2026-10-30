package config

import "log/slog"

// String redacts credentials in diagnostics.
func (c Auth) String() string { return "Auth{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Auth) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Auth) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c RsyncSettings) String() string { return "RsyncSettings{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c RsyncSettings) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c RsyncSettings) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c GitHubSettings) String() string { return "GitHubSettings{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c GitHubSettings) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c GitHubSettings) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c Rsync) String() string { return "Rsync{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Rsync) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Rsync) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c Notification) String() string { return "Notification{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Notification) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Notification) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c Settings) String() string { return "Settings{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Settings) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Settings) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c Source) String() string { return "Source{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Source) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Source) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c Config) String() string { return "Config{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c Config) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c Config) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c *Manager) String() string { return "Manager{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c *Manager) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c *Manager) LogValue() slog.Value { return slog.StringValue(c.String()) }

// String redacts credentials in diagnostics.
func (c SaveOptions) String() string { return "SaveOptions{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (c SaveOptions) GoString() string { return c.String() }

// LogValue redacts credentials in structured logs.
func (c SaveOptions) LogValue() slog.Value { return slog.StringValue(c.String()) }
