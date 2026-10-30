package run

import "log/slog"

// String hides new credentials supplied with a configuration update.
func (options ApplyConfigOptions) String() string { return "ApplyConfigOptions{redacted}" }

// GoString hides credentials in Go-syntax diagnostics.
func (options ApplyConfigOptions) GoString() string { return options.String() }

// LogValue hides credentials in structured logging.
func (options ApplyConfigOptions) LogValue() slog.Value { return slog.StringValue(options.String()) }
