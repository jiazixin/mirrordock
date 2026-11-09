import { clientError, errorText } from './errors'
import { reactive, readonly, toRaw } from 'vue'
import { api, ApiError, cancelSessionRequests, taggedApi, decodeProgress } from './api'
import { warningText } from './error-text'
import type { ConfigView, Configuration, Run, SystemInfo } from './types'

type AppState = {
  config: ConfigView | null
  runs: Run[]
  liveRuns: Run[]
  info: SystemInfo | null
  username: string
  toasts: { id: number; message: string; tone: 'success' | 'error' | 'info' }[]
  confirmation: (ConfirmRequest & { id: number }) | null
  connected: boolean
  revision: number
  refreshing: boolean
  refreshError: string
}
const mutableState = reactive<AppState>({
  config: null,
  runs: [],
  liveRuns: [],
  info: null,
  username: '',
  toasts: [],
  confirmation: null,
  connected: false,
  revision: 0,
  refreshing: false,
  refreshError: '',
})
export const state = readonly(mutableState)

export function setUsername(username: string) {
  mutableState.username = username
}
export function clearToasts() {
  mutableState.toasts = []
}
export function setConfiguration(config: ConfigView) {
  mutableState.config = config
}

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
  mutableState.refreshing = true
  mutableState.refreshError = ''
  try {
    const [config, runs, info] = await Promise.all([
      taggedApi<ConfigView>({ scope: 'session', path: '/config', signal: controller.signal }),
      api<{ items: Run[] }>({
        scope: 'session',
        path: '/runs?latest=true',
        signal: controller.signal,
      }),
      api<SystemInfo>({ scope: 'session', path: '/system/info', signal: controller.signal }),
    ])
    if (current !== refreshID || controller.signal.aborted) return
    mutableState.config = { ...config.data, revision: config.etag }
    // While connected, the event stream is the only ordered source of run states;
    // mixing this response with older stream frames made states jump back.
    if (!mutableState.connected) mutableState.runs = runs.items
    mutableState.info = info
    mutableState.revision++
  } catch (error) {
    if (!controller.signal.aborted && current === refreshID) {
      mutableState.refreshError = errorText(error)
      throw error
    }
  } finally {
    if (current === refreshID) mutableState.refreshing = false
  }
}

export async function saveConfiguration({
  config,
  token,
  proxy,
  revision = mutableState.config?.revision,
  signal,
}: {
  config: Configuration
  token?: string
  proxy?: string
  revision?: string
  signal?: AbortSignal
}) {
  if (!revision) throw clientError('config_revision_missing')
  refreshController?.abort()
  refreshID++
  const session = sessionID
  const { data: saved, etag } = await taggedApi<ConfigView>({
    path: '/config',
    method: 'PUT',
    signal,
    headers: { 'If-Match': revision },
    body: {
      config,
      ...(token === undefined ? {} : { token_update: token }),
      ...(proxy === undefined ? {} : { proxy_update: proxy }),
    },
  })
  if (session !== sessionID) return
  mutableState.config = { ...saved, revision: etag }
  await refresh()
  if (session !== sessionID) return
  // The YAML is already written even when follow-up integration failed.
  if (saved.warning) notify(warningText(saved.warning), 'error')
  else notify('配置已写回 YAML' + (saved.restart_required ? '，部分设置重启生效' : ''))
}

export async function reloadConfiguration() {
  const session = sessionID
  const { data: loaded, etag } = await taggedApi<ConfigView>({
    path: '/config/reload',
    method: 'POST',
  })
  if (session !== sessionID) return
  mutableState.config = { ...loaded, revision: etag }
  await refresh()
  if (session !== sessionID) return
  if (loaded.warning) notify(warningText(loaded.warning), 'error')
  else notify('已重新载入磁盘配置')
}

export function connectEvents() {
  disconnectEvents()
  const current = new EventSource('/api/events')
  stream = current
  current.addEventListener('progress', (event) => {
    if (stream !== current) return
    let payload
    try {
      payload = decodeProgress(event.data)
    } catch (error: unknown) {
      mutableState.refreshError = errorText(error)
      mutableState.connected = false
      return
    }
    mutableState.liveRuns = payload.runs
    mutableState.runs = payload.runs
    if (mutableState.info) {
      mutableState.info.downloads = payload.downloads
      mutableState.info.publications = payload.publications
      mutableState.info.schedules = payload.schedules
    }
    mutableState.connected = true
  })
  current.onerror = async () => {
    if (stream !== current) return
    mutableState.connected = false
    try {
      const status = await api<{ authenticated: boolean }>({
        path: '/auth/session',
        scope: 'session',
      })
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
  mutableState.liveRuns = []
  mutableState.connected = false
}

export function resetState() {
  cancelSessionRequests()
  sessionID++
  refreshID++
  refreshController?.abort()
  disconnectEvents()
  settleConfirmation(false)
  mutableState.toasts = []
  mutableState.username = ''
  mutableState.config = null
  mutableState.runs = []
  mutableState.info = null
  mutableState.refreshError = ''
  mutableState.refreshing = false
}

let toastId = 0
export function dismissToast(id: number) {
  mutableState.toasts = mutableState.toasts.filter((item) => item.id !== id)
}
export function notify(message: string, tone: 'success' | 'error' | 'info' = 'success') {
  mutableState.toasts = [...mutableState.toasts.slice(-2), { id: ++toastId, message, tone }]
}
export async function perform<T>(operation: () => Promise<T>): Promise<T | undefined> {
  try {
    return await operation()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') return undefined
    // Only ApiError has a console text; keep the cause of anything else for diagnosis.
    if (!(error instanceof ApiError)) console.error(error)
    notify(errorText(error), 'error')
    return undefined
  }
}
// crypto.randomUUID exists only in secure contexts, and the console is often opened over
// plain HTTP on a LAN address; getRandomValues is available there too.
function idempotencyKey(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16))
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('')
}
export async function runSource(id: string) {
  // One key per click: a retried request replays the first result instead
  // of queueing a second run.
  await api<Run>({
    path: `/sources/${encodeURIComponent(id)}/runs`,
    method: 'POST',
    headers: { 'Idempotency-Key': idempotencyKey() },
  })
  notify('同步任务已加入队列')
  await refresh()
}
export type ConfirmRequest = {
  title: string
  message: string
  confirmLabel: string
  cancelLabel?: string
  tone?: 'primary' | 'amber' | 'red'
}
// Resolves through the shell's ConfirmDialog; a newer request or logout declines the pending one.
export function confirmAction(request: ConfirmRequest): Promise<boolean> {
  settleConfirmation(false)
  mutableState.confirmation = { ...request, id: ++confirmID }
  return new Promise((resolve) => {
    confirmResolve = resolve
  })
}
export function settleConfirmation(accepted: boolean) {
  const resolve = confirmResolve
  confirmResolve = undefined
  mutableState.confirmation = null
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
  await api<undefined>({ path: `/runs/${run.id}/${action}`, method: 'POST' })
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
type Mutable<T> = { -readonly [K in keyof T]: Mutable<T[K]> }
/** Copies the plain API state into a mutable form draft; toRaw removes Vue's proxy. */
export function clone<T>(value: T): Mutable<T> {
  return structuredClone(toRaw(value))
}
