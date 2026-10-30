import type { SourceType } from './types'
const sourceTypeLabels: Record<SourceType, string> = {
  rsync: 'Rsync',
  reposync: 'Reposync',
  github_release: 'GitHub Release',
}
export function sourceType(type: SourceType) {
  return sourceTypeLabels[type]
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
