// Package timefmt provides the time type used in the API and in stored run
// documents:
// a UTC instant with millisecond precision, written as 2006-01-02T15:04:05.000Z.
package timefmt

import (
	"fmt"
	"time"
)

// Layout is the only accepted text form. The literal Z does not convert time
// zones, so values are converted to UTC before formatting.
const Layout = "2006-01-02T15:04:05.000Z"

// Time wraps time.Time instead of embedding it, because an embedded
// time.Time would promote MarshalJSON and bypass the fixed layout.
type Time struct {
	t time.Time
}

// From returns t truncated to milliseconds in UTC.
func From(t time.Time) Time {
	return Time{t: t.UTC().Truncate(time.Millisecond)}
}

// Parse reads a value in Layout.
func Parse(s string) (Time, error) {
	t, err := time.Parse(Layout, s)
	if err != nil {
		return Time{}, fmt.Errorf("parse time %q: %w", s, err)
	}
	if t.UTC().Format(Layout) != s {
		return Time{}, fmt.Errorf("parse time %q: noncanonical UTC millisecond timestamp", s)
	}
	return Time{t: t}, nil
}

// Time returns the instant as a time.Time in UTC.
func (t Time) Time() time.Time {
	return t.t
}

// IsZero reports whether t is the zero instant; JSON omitzero relies on it.
func (t Time) IsZero() bool {
	return t.t.IsZero()
}

// String formats t in Layout.
func (t Time) String() string {
	return t.t.UTC().Format(Layout)
}

// MarshalText implements encoding.TextMarshaler.
func (t Time) MarshalText() ([]byte, error) {
	return t.t.UTC().AppendFormat(nil, Layout), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (t *Time) UnmarshalText(text []byte) error {
	parsed, err := Parse(string(text))
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
