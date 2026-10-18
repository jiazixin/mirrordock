import { state } from './state'
import type { SourceType } from './types'
export function time(value?: string) {
  if (!value || value.startsWith('0001')) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  const timezone = state.info?.timezone
  try {
    if (timezone && timezone !== 'Local')
      return new Intl.DateTimeFormat('zh-CN', {
        timeZone: timezone,
        year: 'numeric',
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(date)
  } catch {
    /* use startup offset */
  }
  const shifted = new Date(date.getTime() + (state.info?.utc_offset_seconds ?? 0) * 1000)
  return shifted.toISOString().slice(0, 19).replace('T', ' ')
}
export function duration(seconds?: number | null) {
  if (seconds == null || seconds < 0) return '—'
  const whole = Math.floor(seconds)
  if (whole < 60) return `${whole}s`
  if (whole < 3600) return `${Math.floor(whole / 60)}m${whole % 60}s`
  return `${Math.floor(whole / 3600)}h${Math.floor((whole % 3600) / 60)}m`
}
export function bytes(size: number) {
  if (!(size >= 1)) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  const index = Math.min(4, Math.floor(Math.log(size) / Math.log(1024)))
  return `${(size / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}`
}
const sourceTypeLabels: Record<SourceType, string> = {
  rsync: 'Rsync',
  reposync: 'Reposync',
  github_release: 'GitHub Release',
}
export function sourceType(type: SourceType) {
  return sourceTypeLabels[type] ?? type
}
export const phases: Record<string, string> = {
  waiting_space: '等待镜像盘空间',
  waiting_downloads: '等待公开下载负载降低',
  retry: '等待重试',
  prepare: '准备目录',
  scan: '扫描来源',
  transfer: '传输文件',
  verify: '校验文件',
  publish: '发布镜像',
  complete: '已完成',
}
// Lifecycle stages shown for a running task: 准备、传输、校验、发布.
export const runStages = ['准备', '传输', '校验', '发布']
const phaseStages: Record<string, number> = {
  prepare: 0,
  scan: 0,
  transfer: 1,
  verify: 2,
  publish: 3,
  complete: 3,
}
// Waiting and retry phases have no lifecycle stage and return -1.
export function runStage(phase: string) {
  return phaseStages[phase] ?? -1
}
