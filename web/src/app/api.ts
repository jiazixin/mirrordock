export class ApiError extends Error { constructor(message: string, public status: number) { super(message) } }
export async function api<T>(path: string, method = 'GET', body?: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, { method, credentials: 'same-origin', headers: { 'X-MirrorDock-Request': '1', ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) }, body: body === undefined ? undefined : JSON.stringify(body) })
  const data = await res.json()
  if (!res.ok) { if (res.status === 401 && !path.startsWith('/auth')) window.dispatchEvent(new Event('session-expired')); throw new ApiError(data.error || `HTTP ${res.status}`, res.status) }
  return data as T
}
