import type { Run, SystemInfo, Publication } from './types'

export type ApiErrorInit = {
  status: number // HTTP status; 0 for client-side failures
  code: string // server error.code, or a client code
  requestId: string // X-Request-ID; empty when unavailable
  details?: unknown
  retryAfterMs?: number // parsed Retry-After
}

/**
 * A failed request. message is English developer text; the console text comes
 * from errorText in errors.ts.
 */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly requestId: string
  readonly details: unknown
  readonly retryAfterMs: number | undefined

  constructor(message: string, init: ApiErrorInit) {
    super(message)
    this.name = 'ApiError'
    this.status = init.status
    this.code = init.code
    this.requestId = init.requestId
    this.details = init.details
    this.retryAfterMs = init.retryAfterMs
  }
}

export type ApiOptions = {
  path: string
  scope?: 'page' | 'session'
  method?: string
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
}

let pageController = new AbortController()
export function cancelPageRequests() {
  pageController.abort()
  pageController = new AbortController()
}

let sessionController = new AbortController()

export function cancelSessionRequests() {
  sessionController.abort()
  sessionController = new AbortController()
}

function retryAfterMs(value: string | null): number | undefined {
  if (!value) return undefined
  const seconds = Number(value)
  if (/^\d+$/.test(value) && Number.isFinite(seconds)) return seconds * 1000
  if (/^[+-]?\d/.test(value) && !value.includes(' ')) return undefined
  const at = Date.parse(value)
  return Number.isNaN(at) ? undefined : Math.max(0, at - Date.now())
}

// No endpoint legitimately takes this long; a request still open after this is
// treated as lost.
const requestTimeoutMs = 60_000
const maxRetries = 2
const maxRetryWaitMs = 10_000

/** Maps a failed fetch: a caller's or session cancel stays a native AbortError. */
function clientFailure(error: unknown, cancel: AbortSignal, timeout: AbortSignal) {
  if (cancel.aborted) return new DOMException('request aborted', 'AbortError')
  if (timeout.aborted)
    return new ApiError('request timed out', { status: 0, code: 'timeout', requestId: '' })
  if (error instanceof TypeError)
    return new ApiError('network request failed', {
      status: 0,
      code: 'network_unavailable',
      requestId: '',
    })
  if (error instanceof ApiError) return error
  return new ApiError('invalid request or response', {
    status: 0,
    code: 'invalid_response',
    requestId: '',
  })
}

/**
 * Request contract §5.3: reads and keyed writes retry network failures, timeouts, 429
 * and 503 at most twice; a Retry-After over 10 s returns the error at once.
 */
function retryDelayMs(error: unknown, attempt: number): number | undefined {
  if (attempt >= maxRetries || !(error instanceof ApiError)) return undefined
  const transient =
    error.code === 'network_unavailable' ||
    error.code === 'timeout' ||
    error.status === 429 ||
    error.status === 503
  if (!transient) return undefined
  if (error.retryAfterMs !== undefined)
    return error.retryAfterMs > maxRetryWaitMs ? undefined : error.retryAfterMs
  return Math.min(maxRetryWaitMs, 500 * 2 ** attempt * (1 + Math.random() * 0.2))
}

function sleep(milliseconds: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const aborted = () => new DOMException('request aborted', 'AbortError')
    if (signal.aborted) {
      reject(aborted())
      return
    }
    const stop = () => {
      clearTimeout(timer)
      reject(aborted())
    }
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', stop)
      resolve()
    }, milliseconds)
    signal.addEventListener('abort', stop, { once: true })
  })
}

/** A response body with the entity tag the server sent, if any. */
export type Tagged<T> = { data: T; etag: string }

/** Sends a request to the Go server; the response is asserted to be T. */
export async function api<T>(options: ApiOptions): Promise<T> {
  return (await taggedApi<T>(options)).data
}

/** Like api, and also returns the ETag that optimistic writes send as If-Match. */
export async function taggedApi<T>(options: ApiOptions): Promise<Tagged<T>> {
  const { path } = options
  const method = (options.method ?? 'GET').toUpperCase()
  const session = sessionController
  const cancel = AbortSignal.any([
    session.signal,
    ...(options.scope === 'session' ? [] : [pageController.signal]),
    ...(options.signal ? [options.signal] : []),
  ])
  const retryable =
    method === 'GET' ||
    Object.entries(options.headers ?? {}).some(
      ([name, value]) => name.toLowerCase() === 'idempotency-key' && value.trim().length > 0,
    )
  for (let attempt = 0; ; attempt++) {
    try {
      return await send<T>(path, options, cancel)
    } catch (error: unknown) {
      const delay = retryable ? retryDelayMs(error, attempt) : undefined
      if (delay === undefined) throw error
      await sleep(delay, cancel)
    }
  }
}

async function send<T>(path: string, options: ApiOptions, cancel: AbortSignal): Promise<Tagged<T>> {
  const { method = 'GET', body, headers = {} } = options
  const timeout = AbortSignal.timeout(requestTimeoutMs)
  const requestSignal = AbortSignal.any([cancel, timeout])
  let res: Response
  try {
    res = await fetch(`/api${path}`, {
      method,
      signal: requestSignal,
      credentials: 'same-origin',
      redirect: 'error',
      headers: {
        ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
        ...headers,
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  } catch (error: unknown) {
    throw clientFailure(error, cancel, timeout)
  }
  cancel.throwIfAborted()
  if (res.status === 401 && !path.startsWith('/auth'))
    window.dispatchEvent(new Event('session-expired'))
  const requestId = res.headers.get('X-Request-ID') ?? ''
  const etag = res.headers.get('ETag') ?? ''
  if (res.status === 204) return { data: undefined as T, etag }
  let data: unknown
  try {
    data = await res.json()
  } catch (error: unknown) {
    if (requestSignal.aborted) throw clientFailure(error, cancel, timeout)
    throw new ApiError(`response is not JSON (HTTP ${res.status})`, {
      status: res.status,
      code: 'invalid_response',
      requestId,
    })
  }
  cancel.throwIfAborted()
  if (!res.ok) {
    const envelope = typeof data === 'object' && data !== null ? (data as { error?: unknown }) : {}
    const error =
      typeof envelope.error === 'object' && envelope.error !== null
        ? (envelope.error as { code?: unknown; message?: unknown; details?: unknown })
        : {}
    throw new ApiError(typeof error.message === 'string' ? error.message : `HTTP ${res.status}`, {
      status: res.status,
      code: typeof error.code === 'string' ? error.code : '',
      requestId,
      details: error.details,
      retryAfterMs: retryAfterMs(res.headers.get('Retry-After')),
    })
  }
  return { data: data as T, etag }
}

/** Same-repository SSE contract; assertions stay at the API boundary. */
export function decodeProgress(text: string): {
  runs: Run[]
  downloads: SystemInfo['downloads']
  publications: Publication[]
  schedules: Record<string, string>
} {
  try {
    const data: unknown = JSON.parse(text)
    if (
      typeof data !== 'object' ||
      data === null ||
      !('runs' in data) ||
      !Array.isArray(data.runs) ||
      !('downloads' in data) ||
      !('publications' in data) ||
      !('schedules' in data)
    )
      throw new Error('invalid progress envelope')
    return data as ReturnType<typeof decodeProgress>
  } catch {
    throw new ApiError('invalid progress response', {
      status: 0,
      code: 'invalid_response',
      requestId: '',
    })
  }
}
