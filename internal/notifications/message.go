package notifications

import (
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"

	"mirrordock/internal/runs"
)

func short(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	chars := []rune(value)
	if len(chars) > 100 {
		return string(chars[:100]) + "…"
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
	p := r.Progress
	total := p.Files
	count := fmt.Sprintf("总数：%d · 成功：%d（其中复用：%d） · 失败：%d · 未完成：%d", total, p.Success, p.Skipped, p.Failed, max(total-p.Success-p.Failed, 0))
	if r.Type == "rsync" {
		count = fmt.Sprintf("条目总数（含目录及未变化文件）：%d · 本次传输普通文件：%d\n退出失败不推算失败或未完成文件数。", total, p.Success)
	} else if r.Type == "reposync" {
		count = "包数量：执行器未提供可靠统计；不推算失败或未完成文件数。"
	}
	settings := r.Snapshot.Settings
	detail := fmt.Sprintf("累计预算 %s", settings.TaskTimeout)
	switch r.Type {
	case "rsync":
		detail += fmt.Sprintf("；连接 %s；I/O %s；限速 %g MB/s", settings.Rsync.ConnectTimeout, settings.Rsync.IOTimeout, settings.Rsync.LimitMB)
	case "github_release":
		detail += fmt.Sprintf("；最近 Release 默认 %d（源覆盖 %d）", settings.GitHub.ReleaseCount, r.Snapshot.Source.GitHubRelease.ReleaseCount)
	case "reposync":
		detail += fmt.Sprintf("；发行版 %s；架构 %s", short(r.Snapshot.Source.RepoSync.Releasever), short(r.Snapshot.Source.RepoSync.Basearch))
	}
	body := fmt.Sprintf("任务编号：#%d\n来源：%s（%s）\n处理目标：/%s/\n执行器：%s · 触发：%s\n执行配置：%s\n%s\n提交时间：%s\n实际开始：%s\n完成时间：%s\n处理耗时：%s", r.ID, short(r.Name), short(r.SourceID), short(r.Dest), short(r.Type), short(r.Trigger), detail, count, formatTime(r.CreatedAt, location, "未记录"), formatTime(r.StartedAt, location, "未开始"), formatTime(r.EndedAt, location, "未记录"), duration)
	return Message{Event: "batch.finished", Title: "[" + Project + "] · " + title, Body: body}, failed
}
