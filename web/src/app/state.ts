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
  connected: false,
  revision: 0,
})
let stream: EventSource | null = null
let refreshID = 0
let refreshController: AbortController | undefined
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
    state.runs = runs.items
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
const runActionPrompts = {
  pause: (name: string) => `暂停任务“${name}”？当前传输会停止，工作目录保留，之后可继续。`,
  resume: (name: string) => `继续任务“${name}”？将沿用剩余运行预算。`,
  cancel: (name: string) =>
    `取消任务“${name}”？取消后无法继续，未发布的工作目录会被清理，已发布镜像不受影响。`,
}
export async function runAction(
  run: Pick<Run, 'id' | 'name'>,
  action: keyof typeof runActionPrompts,
) {
  if (!window.confirm(runActionPrompts[action](run.name))) return
  await api(`/runs/${run.id}/${action}`, 'POST')
  await refresh()
}
export async function rerunSource(id: string, name: string) {
  if (!window.confirm(`重新同步“${name}”？将新建一次同步任务。`)) return
  await runSource(id)
}
export function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}
