package notification

import "log/slog"

// String redacts credentials in diagnostics.
func (v Update) String() string { return "Update{redacted}" }

// GoString redacts credentials in Go-syntax diagnostics.
func (v Update) GoString() string { return v.String() }

// LogValue redacts credentials in structured logs.
func (v Update) LogValue() slog.Value { return slog.StringValue(v.String()) }

// String redacts the service's encryption key and channel state.
func (s *Service) String() string { return "Service{redacted}" }

// GoString redacts the service in Go-syntax diagnostics.
func (s *Service) GoString() string { return s.String() }

// LogValue redacts the service in structured logging.
func (s *Service) LogValue() slog.Value { return slog.StringValue(s.String()) }
