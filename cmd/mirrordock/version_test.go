package main

import (
	"runtime/debug"
	"testing"
)

func TestReadBuild(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	vcs := func(modified string) []debug.BuildSetting {
		return []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.time", Value: "2026-10-01T12:00:00Z"},
			{Key: "vcs.modified", Value: modified},
		}
	}
	for _, tc := range []struct {
		name    string
		release string
		info    *debug.BuildInfo
		want    string
	}{
		{"no metadata", "dev", nil, "dev"},
		{"release without metadata", "v1.2.0", nil, "v1.2.0"},
		{"release with VCS", "v1.2.0", &debug.BuildInfo{Settings: vcs("false")}, "v1.2.0 (revision=0123456789ab)"},
		{"module tag", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v1.2.0"}, Settings: vcs("false")}, "v1.2.0 (revision=0123456789ab)"},
		{"pseudo-version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001120000-0123456789ab"}, Settings: vcs("false")}, "v0.0.0-20261001120000-0123456789ab"},
		{"dirty pseudo-version", "dev", &debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001120000-0123456789ab+dirty"}, Settings: vcs("true")}, "v0.0.0-20261001120000-0123456789ab+dirty (modified)"},
		{"devel checkout", "dev", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs("true")}, "0123456789ab (modified)"},
		{"blank release", " ", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, "dev"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readBuild(tc.release, tc.info).String(); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadBuildKeepsCommitTime(t *testing.T) {
	b := readBuild("dev", &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.time", Value: "2026-10-01T12:00:00Z"}}})
	if b.Time != "2026-10-01T12:00:00Z" || b.Revision != "" || b.Version != "dev" {
		t.Fatalf("got %+v", b)
	}
}
