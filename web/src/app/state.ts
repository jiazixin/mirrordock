import { reactive } from 'vue'
import { api, cancelSessionRequests } from './api'
import type { ConfigView, Configuration, Publication, Run, SystemInfo } from './types'

export const state = reactive({
  config: null as ConfigView | null,
  runs: [] as Run[],
  liveRuns: [] as Run[],
  info: null as SystemInfo | null,
  username: '',
  toasts: [] as { id: number; message: string; tone: 'success' | 'error' | 'info' }[],
  confirmation: null as (ConfirmRequest & { id: number }) | null,
  connected: false,
  revision: 0,
})
let stream: EventSource | null = null
let refreshID = 0
let refreshController: AbortController | undefined
let confirmResolve: ((accepted: boolean) => void) | undefined
let confirmID = 0
let sessionID = 0

export async function refresh() {
  refreshController?.abort()
  const controller = new AbortController()
  refreshController = controller
  const current = ++refreshID
  try {
    const [config, runs, info] = await Promise.all([
      api<ConfigView>('/config', 'GET', undefined, controller.signal),
      api<{ items: Run[] }>('/runs?latest=1', 'GET', undefined, controller.signal),
      api<SystemInfo>('/system/info', 'GET', undefined, controller.signal),
    ])
    if (current !== refreshID || controller.signal.aborted) return
    state.config = config
    // While connected, the event stream is the only ordered source of run states;
    // mixing this response with older stream frames made states jump back.
    if (!state.connected) state.runs = runs.items
    state.info = info
    state.revision++
  } catch (error) {
    if (!controller.signal.aborted) throw error
  }
}

export async function saveConfiguration(
  config: Configuration,
  token?: string,
  proxy?: string,
  revision = state.config?.revision,
) {
  if (!revision) throw new Error('请重新登录后保存配置')
  refreshController?.abort()
  refreshID++
  const session = sessionID
  const saved = await api<ConfigView>('/config', 'PUT', {
    config,
    revision,
    token_update: token ?? null,
    proxy_update: proxy ?? null,
  })
  if (session !== sessionID) return
  state.config = saved
  await refresh()
  if (session !== sessionID) return
  // The YAML is already written even when follow-up integration failed.
  if (saved.warning) notify(saved.warning, 'error')
  else notify('配置已写回 YAML' + (saved.restart_required ? '，部分设置重启生效' : ''))
}

export async function reloadConfiguration() {
  const session = sessionID
  const loaded = await api<ConfigView>('/config/reload', 'POST')
  if (session !== sessionID) return
  state.config = loaded
  await refresh()
  if (session !== sessionID) return
  if (loaded.warning) notify(loaded.warning, 'error')
  else notify('已重新载入磁盘配置')
}

export function connectEvents() {
  disconnectEvents()
  const current = new EventSource('/api/events')
  stream = current
  current.addEventListener('progress', (event) => {
    if (stream !== current) return
    const payload = JSON.parse((event as MessageEvent).data) as {
      runs: Run[]
      downloads: SystemInfo['downloads']
      publications: Publication[]
      schedules: Record<string, string>
    }
    state.liveRuns = payload.runs
    state.runs = payload.runs
    if (state.info) {
      state.info.downloads = payload.downloads
      state.info.publications = payload.publications
      state.info.schedules = payload.schedules
    }
    state.connected = true
  })
  current.onerror = async () => {
    if (stream !== current) return
    state.connected = false
    try {
      const status = await api<{ authenticated: boolean }>('/auth/status')
      if (stream === current && !status.authenticated)
        window.dispatchEvent(new Event('session-expired'))
    } catch {
      /* EventSource retries temporary network failures. */
    }
  }
}

export function disconnectEvents() {
  stream?.close()
  stream = null
  state.liveRuns = []
  state.connected = false
}

export function resetState() {
  cancelSessionRequests()
  sessionID++
  refreshID++
  refreshController?.abort()
  disconnectEvents()
  settleConfirmation(false)
  state.toasts = []
  state.username = ''
  state.config = null
  state.runs = []
  state.info = null
}

let toastId = 0
export function dismissToast(id: number) {
  state.toasts = state.toasts.filter((item) => item.id !== id)
}
export function notify(message: string, tone: 'success' | 'error' | 'info' = 'success') {
  state.toasts = [...state.toasts.slice(-2), { id: ++toastId, message, tone }]
}
export async function perform<T>(operation: () => Promise<T>): Promise<T | undefined> {
  try {
    return await operation()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') return undefined
    notify(error instanceof Error ? error.message : String(error), 'error')
    return undefined
  }
}
export async function runSource(id: string) {
  await api(`/sources/${encodeURIComponent(id)}/run`, 'POST')
  notify('同步任务已加入队列')
  await refresh()
}
export interface ConfirmRequest {
  title: string
  message: string
  confirmLabel: string
  cancelLabel?: string
  tone?: 'primary' | 'amber' | 'red'
}
// Resolves through the shell's ConfirmDialog; a newer request or logout declines the pending one.
export function confirmAction(request: ConfirmRequest): Promise<boolean> {
  settleConfirmation(false)
  state.confirmation = { ...request, id: ++confirmID }
  return new Promise((resolve) => {
    confirmResolve = resolve
  })
}
export function settleConfirmation(accepted: boolean) {
  const resolve = confirmResolve
  confirmResolve = undefined
  state.confirmation = null
  resolve?.(accepted)
}
const runActionPrompts = {
  pause: (name: string): ConfirmRequest => ({
    title: '暂停任务',
    message: `暂停“${name}”？当前传输会停止，工作目录保留，之后可继续。`,
    confirmLabel: '暂停',
    tone: 'amber',
  }),
  resume: (name: string): ConfirmRequest => ({
    title: '继续任务',
    message: `继续“${name}”？将沿用剩余运行预算。`,
    confirmLabel: '继续',
  }),
  cancel: (name: string): ConfirmRequest => ({
    title: '取消任务',
    message: `取消“${name}”？取消后无法继续；已下载的内容保留 7 天，该源下次同步会接着使用，已发布镜像不受影响。`,
    confirmLabel: '取消任务',
    cancelLabel: '保留任务',
    tone: 'red',
  }),
}
export async function runAction(
  run: Pick<Run, 'id' | 'name'>,
  action: keyof typeof runActionPrompts,
) {
  if (!(await confirmAction(runActionPrompts[action](run.name)))) return
  await api(`/runs/${run.id}/${action}`, 'POST')
  await refresh()
}
export async function rerunSource(id: string, name: string) {
  const accepted = await confirmAction({
    title: '重新同步',
    message: `重新同步“${name}”？将新建一次同步任务。`,
    confirmLabel: '重新同步',
  })
  if (accepted) await runSource(id)
}
export function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}
