package syncer

import (
	"math"
	"testing"
)

func FuzzParseProgress(f *testing.F) {
	for _, seed := range []string{"1,234 42% 1.50MB/s 0:01:02", "1 999% 0B/s 0:0:0", "invalid", "1 1% 1B/s :"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		if len(line) > 64<<10 {
			return
		}
		progress, ok := ParseProgress(line)
		if !ok {
			return
		}
		if progress.Percent == nil || *progress.Percent < 0 || *progress.Percent > 100 || math.IsNaN(progress.Speed) || math.IsInf(progress.Speed, 0) {
			t.Fatalf("got invalid parsed progress %+v", progress)
		}
	})
}
