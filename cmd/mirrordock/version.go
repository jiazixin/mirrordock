package main

import (
	"cmp"
	"runtime/debug"
	"strings"
)

// version may be set for builds without VCS metadata, such as Docker builds:
//
//	go build -ldflags "-X main.version=v1.0.0" ./cmd/mirrordock
//
// Otherwise the module version Go stamps from VCS is used, then the revision.
var version = "dev"

// buildInfo describes the running binary.
type buildInfo struct {
	Version  string // release, module version, short revision or "dev"
	Revision string // short VCS revision; empty when unknown
	Time     string // VCS commit time in RFC 3339; empty when unknown
	Modified bool   // built from a working tree with uncommitted changes
}

func currentBuild() buildInfo {
	info, _ := debug.ReadBuildInfo()
	return readBuild(version, info)
}

func readBuild(release string, info *debug.BuildInfo) buildInfo {
	var b buildInfo
	var moduleVersion string
	if info != nil {
		moduleVersion = info.Main.Version
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				b.Revision = s.Value[:min(12, len(s.Value))]
			case "vcs.time":
				b.Time = s.Value
			case "vcs.modified":
				b.Modified = s.Value == "true"
			}
		}
	}
	b.Version = cmp.Or(usableVersion(release), usableVersion(moduleVersion), b.Revision, "dev")
	return b
}

func usableVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "dev" || v == "(devel)" {
		return ""
	}
	return v
}

// String formats the version as "v1.0.0 (revision=0123456789ab, modified)".
func (b buildInfo) String() string {
	var details []string
	// A VCS-derived module version already embeds the revision.
	if b.Revision != "" && !strings.Contains(b.Version, b.Revision) {
		details = append(details, "revision="+b.Revision)
	}
	if b.Modified {
		details = append(details, "modified")
	}
	if len(details) == 0 {
		return b.Version
	}
	return b.Version + " (" + strings.Join(details, ", ") + ")"
}
