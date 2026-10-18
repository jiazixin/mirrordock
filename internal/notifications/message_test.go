package notifications

import (
	"mirrordock/internal/runs"
	syncer "mirrordock/internal/sync"
	"strings"
	"testing"
	"time"
)

func TestNotificationUsesCumulativeDuration(t *testing.T) {
	r := &runs.Run{State: "succeeded", Type: "github_release", StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now(), ElapsedSeconds: 10}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "处理耗时：10s") {
		t.Fatalf("累计执行10秒却按开始/结束差显示一小时: %s", m.Body)
	}
}

func TestNotificationReuseIsSuccessSubset(t *testing.T) {
	r := &runs.Run{State: "failed", Type: "github_release", Progress: syncer.Progress{Files: 3, Success: 2, Skipped: 2}}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "未完成：1") {
		t.Fatalf("3个资产中复用2个后失败，剩余1个却显示0: %s", m.Body)
	}
}

func TestNotificationMissingElapsedDoesNotUseWallClock(t *testing.T) {
	r := &runs.Run{State: "succeeded", StartedAt: time.Now().Add(-time.Hour), EndedAt: time.Now()}
	m, _ := resultMessage(r, "UTC")
	if !strings.Contains(m.Body, "处理耗时：未记录") {
		t.Fatal(m.Body)
	}
}
