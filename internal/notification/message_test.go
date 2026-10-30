package notification

import (
	"strings"
	"testing"
	"time"

	runengine "mirrordock/internal/run"
	"mirrordock/internal/syncer"
	"mirrordock/internal/timefmt"
)

func TestNotificationUsesCumulativeDuration(t *testing.T) {
	t.Parallel()
	r := &runengine.Run{State: "succeeded", Type: "github_release", StartedAt: timefmt.From(time.Now().Add(-time.Hour)), EndedAt: timefmt.From(time.Now()), ElapsedMillis: 10000}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "处理耗时：10s") {
		t.Fatalf("累计执行10秒却按开始/结束差显示一小时: %s", m.Body)
	}
}

func TestNotificationReuseIsSuccessSubset(t *testing.T) {
	t.Parallel()
	r := &runengine.Run{State: "failed", Type: "github_release", Progress: syncer.Progress{Files: 3, Success: 2, Skipped: 2}}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "未完成：1") {
		t.Fatalf("3个资产中复用2个后失败，剩余1个却显示0: %s", m.Body)
	}
}

func TestNotificationMissingElapsedDoesNotUseWallClock(t *testing.T) {
	t.Parallel()
	r := &runengine.Run{State: "succeeded", StartedAt: timefmt.From(time.Now().Add(-time.Hour)), EndedAt: timefmt.From(time.Now())}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "处理耗时：未记录") {
		t.Fatal(m.Body)
	}
}
