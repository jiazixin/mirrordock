package app

import (
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"
)

func startupLocation() *time.Location {
	if time.Local.String() != "Local" {
		return time.Local
	}
	// Preserve the process's zone, but expose its IANA name when the OS uses a
	// zoneinfo symlink so browser formatting retains historical DST behavior.
	if real, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(real, "/zoneinfo/"); ok {
			if location, err := time.LoadLocation(name); err == nil {
				return location
			}
		}
	}
	return time.Local
}
