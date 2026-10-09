package main

import (
	"runtime/debug"
	"testing"
)

func TestFormatVersion(t *testing.T) {
	for _, tc := range []struct {
		name, release, want string
		info                *debug.BuildInfo
	}{
		{name: "source archive", release: "dev", want: "dev"},
		{name: "empty override", release: "  ", want: "dev"},
		{name: "explicit release", release: "1.2.3", want: "1.2.3"},
		{name: "no VCS metadata", release: "dev", info: &debug.BuildInfo{}, want: "dev"},
		{
			name: "clean checkout", release: "dev", want: "0123456789ab",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
				{Key: "vcs.modified", Value: "false"},
			}},
		},
		{
			name: "modified release checkout", release: "1.2.3", want: "1.2.3 (revision=0123456789ab, modified)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.modified", Value: "true"},
				{Key: "vcs.revision", Value: "0123456789abcdef"},
			}},
		},
		{
			name: "short revision", release: "dev", want: "abc",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
		},
		{
			name: "modified without revision", release: "dev", want: "dev (modified)",
			info: &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatVersion(tc.release, tc.info); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
