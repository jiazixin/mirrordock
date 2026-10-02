package app

import (
	"testing"
	"time"
)

func TestRunTimeRangesUseStartupCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 9, 2, 30, 0, 0, loc)
	from, until, err := runTimeRange("yesterday", now, loc)
	if err != nil || from.Day() != 8 || from.Hour() != 0 || until.Day() != 9 || until.Sub(from) != 23*time.Hour {
		t.Fatal(from, until, err)
	}
	from, until, err = runTimeRange("month", now, loc)
	if err != nil || from.Day() != 1 || from.Month() != time.March || from.Hour() != 0 || !until.IsZero() {
		t.Fatal(from, until, err)
	}
	for key, hours := range map[string]int{"1h": 1, "12h": 12, "24h": 24, "3d": 72, "7d": 168, "30d": 720} {
		from, until, err := runTimeRange(key, now, loc)
		if err != nil || now.Sub(from) != time.Duration(hours)*time.Hour || !until.IsZero() {
			t.Fatal(key, from, until, err)
		}
	}
	if _, _, err := runTimeRange("invalid", now, loc); err == nil {
		t.Fatal("accepted invalid range")
	}
}
