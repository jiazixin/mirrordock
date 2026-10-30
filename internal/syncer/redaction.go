package syncer

import "log/slog"

// String redacts credentials in diagnostics.
func (v Task) String() string { return "Task{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (v Task) GoString() string { return v.String() }

// LogValue redacts credentials in structured logs.
func (v Task) LogValue() slog.Value { return slog.StringValue(v.String()) }
