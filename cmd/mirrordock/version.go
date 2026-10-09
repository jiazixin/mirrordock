package main

import (
	"runtime/debug"
	"strings"
)

// Release builds may set this with -ldflags '-X main.version=<release>'.
// Direct builds fall back to embedded VCS metadata, or dev without metadata.
var version = "dev"

func versionString() string {
	info, _ := debug.ReadBuildInfo()
	return formatVersion(version, info)
}

func formatVersion(release string, info *debug.BuildInfo) string {
	release = strings.TrimSpace(release)
	if release == "" {
		release = "dev"
	}
	if info == nil {
		return release
	}
	var revision string
	var modified bool
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	var details []string
	if revision != "" {
		revision = revision[:min(12, len(revision))]
		if release == "dev" {
			release = revision
		} else if release != revision {
			details = append(details, "revision="+revision)
		}
	}
	if modified {
		details = append(details, "modified")
	}
	if len(details) == 0 {
		return release
	}
	return release + " (" + strings.Join(details, ", ") + ")"
}
