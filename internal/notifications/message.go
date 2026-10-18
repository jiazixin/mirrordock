package notifications

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"

	"mirrordock/internal/runs"
)

// short removes control characters and truncates value to 100 characters.
func short(value string) string {
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	characters := []rune(value)
	if len(characters) > 100 {
		return string(characters[:100]) + "…"
	}
	return value
}

func formatTime(value time.Time, location *time.Location, missing string) string {
	if value.IsZero() {
		return missing
	}
	return value.In(location).Format("2006-01-02 15:04:05 UTC-07:00") + " (" + location.String() + ")"
}

func resultMessage(r *runs.Run, zone string) (Message, bool) {
	failed := r.Progress.Failed > 0 || r.State == "failed" || r.State == "timed_out" || r.State == "interrupted"
	title := "任务成功"
	switch r.State {
	case "failed", "timed_out":
		title = "任务失败"
		if r.Progress.Success > 0 {
			title = "任务失败（部分失败）"
		}
	case "interrupted":
		title = "任务失败（服务中断）"
	case "cancelled":
		title = "任务已取消"
	case "succeeded":
		if failed {
			title = "任务失败（部分失败）"
		}
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		location = time.UTC
	}
	duration := "未开始"
	if !r.StartedAt.IsZero() {
		duration = "未记录"
		if r.ElapsedSeconds > 0 {
			duration = time.Duration(r.ElapsedSeconds * float64(time.Second)).Round(time.Second).String()
		}
	}
	lines := []string{
		fmt.Sprintf("任务编号：#%d", r.ID),
		fmt.Sprintf("来源：%s（%s）", short(r.Name), short(r.SourceID)),
		fmt.Sprintf("处理目标：/%s/", short(r.Dest)),
		fmt.Sprintf("执行器：%s · 触发：%s", short(r.Type), short(r.Trigger)),
		"执行配置：" + executionDetail(r),
		fileCounts(r),
		"提交时间：" + formatTime(r.CreatedAt, location, "未记录"),
		"实际开始：" + formatTime(r.StartedAt, location, "未开始"),
		"完成时间：" + formatTime(r.EndedAt, location, "未记录"),
		"处理耗时：" + duration,
	}
	message := Message{
		Event: "batch.finished",
		Title: "[" + Project + "] · " + title,
		Body:  strings.Join(lines, "\n"),
	}
	return message, failed
}

// fileCounts describes only the counters each executor reports reliably.
func fileCounts(r *runs.Run) string {
	progress := r.Progress
	switch r.Type {
	case "rsync":
		return fmt.Sprintf(
			"条目总数（含目录及未变化文件）：%d · 本次传输普通文件：%d\n退出失败不推算失败或未完成文件数。",
			progress.Files, progress.Success,
		)
	case "reposync":
		return "包数量：执行器未提供可靠统计；不推算失败或未完成文件数。"
	}
	unfinished := max(progress.Files-progress.Success-progress.Failed, 0)
	return fmt.Sprintf(
		"总数：%d · 成功：%d（其中复用：%d） · 失败：%d · 未完成：%d",
		progress.Files, progress.Success, progress.Skipped, progress.Failed, unfinished,
	)
}

func executionDetail(r *runs.Run) string {
	settings := r.Snapshot.Settings
	source := r.Snapshot.Source
	detail := "累计预算 " + settings.TaskTimeout
	switch r.Type {
	case "rsync":
		rsync := settings.Rsync
		detail += fmt.Sprintf("；连接 %s；I/O %s；限速 %g MB/s", rsync.ConnectTimeout, rsync.IOTimeout, rsync.LimitMB)
	case "github_release":
		detail += fmt.Sprintf(
			"；最近 Release 默认 %d（源覆盖 %d）",
			settings.GitHub.ReleaseCount, source.GitHubRelease.ReleaseCount,
		)
	case "reposync":
		detail += fmt.Sprintf("；发行版 %s；架构 %s", short(source.RepoSync.Releasever), short(source.RepoSync.Basearch))
	}
	return detail
}
