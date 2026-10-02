package app

import (
	"fmt"
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

func runTimeRange(key string, now time.Time, location *time.Location) (time.Time, time.Time, error) {
	local := now.In(location)
	midnight := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	switch key {
	case "yesterday":
		return midnight.AddDate(0, 0, -1), midnight, nil
	case "month":
		return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, location), time.Time{}, nil
	}
	durations := map[string]time.Duration{"1h": time.Hour, "12h": 12 * time.Hour, "24h": 24 * time.Hour, "3d": 72 * time.Hour, "7d": 168 * time.Hour, "30d": 720 * time.Hour}
	if d, ok := durations[key]; ok {
		return now.Add(-d), time.Time{}, nil
	}
	return time.Time{}, time.Time{}, fmt.Errorf("无效的时间范围: %s", key)
}
