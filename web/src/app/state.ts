import { reactive } from 'vue'
import { api } from './api'
import type { ConfigView, Configuration, Publication, Run, SystemInfo } from './types'

export const state = reactive({ config: null as ConfigView | null, runs: [] as Run[], liveRuns: [] as Run[], info: null as SystemInfo | null, username: '', toasts: [] as { id: number; message: string; tone: 'success' | 'error' | 'info' }[], connected: false, revision: 0 })
let stream: EventSource | null = null
export async function refresh() { const [config, runs, info] = await Promise.all([api<ConfigView>('/config'), api<{ items: Run[] }>('/runs?limit=100'), api<SystemInfo>('/system/info')]); state.config = config; state.runs = runs.items; state.info = info; state.revision++ }
export async function saveConfiguration(config: Configuration, token?: string, proxy?: string) { if (!state.config) return; state.config = await api<ConfigView>('/config', 'PUT', { config, revision: state.config.revision, token_update: token ?? null, proxy_update: proxy ?? null }); await refresh(); notify('配置已写回 YAML' + (state.config?.restart_required ? '，部分设置重启生效' : '')) }
export function connectEvents() { disconnectEvents(); stream = new EventSource('/api/events'); stream.addEventListener('progress', event => { const payload = JSON.parse((event as MessageEvent).data) as { runs: Run[]; downloads: SystemInfo['downloads']; publications: Publication[]; schedules: Record<string,string> }; state.liveRuns = payload.runs; const live = new Map(payload.runs.map(run => [run.id, run])); state.runs = state.runs.map(run => live.get(run.id) ?? run); for (const run of payload.runs) if (!state.runs.some(item => item.id === run.id)) state.runs.push(run); state.runs.sort((a,b) => b.id-a.id); state.runs = state.runs.slice(0,100); if (state.info) { state.info.downloads = payload.downloads; state.info.publications = payload.publications; state.info.schedules = payload.schedules } state.connected = true }); stream.onerror = () => { state.connected = false } }
export function disconnectEvents() { stream?.close(); stream = null; state.liveRuns = []; state.connected = false }
let toastId = 0
export function dismissToast(id: number) { state.toasts = state.toasts.filter(item => item.id !== id) }
export function notify(message: string, tone: 'success' | 'error' | 'info' = 'success') {
  state.toasts = [...state.toasts.slice(-2), { id: ++toastId, message, tone }]
}
export async function perform<T>(operation: () => Promise<T>): Promise<T | undefined> { try { return await operation() } catch (error) { notify(error instanceof Error ? error.message : String(error), 'error'); return undefined } }
export async function runSource(id: string) { await api(`/sources/${encodeURIComponent(id)}/run`, 'POST'); notify('同步任务已加入队列'); await refresh() }
export async function runAction(id: number, action: string) { await api(`/runs/${id}/${action}`, 'POST'); await refresh() }

export function clone<T>(value: T): T { return JSON.parse(JSON.stringify(value)) as T }
