import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, api, taggedApi } from './api'

function respond(status: number, body: unknown, headers: Record<string, string> = {}) {
  vi.stubGlobal(
    'fetch',
    vi.fn(() =>
      Promise.resolve(
        new Response(JSON.stringify(body), {
          status,
          headers: { 'Content-Type': 'application/json', ...headers },
        }),
      ),
    ),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('api', () => {
  it('returns the response body', async () => {
    respond(200, { revision: 3 })
    await expect(api<{ revision: number }>({ path: '/config' })).resolves.toEqual({ revision: 3 })
  })

  it('returns the entity tag with the body', async () => {
    respond(200, { file: 'config.yaml' }, { ETag: '"r1"' })
    await expect(taggedApi<{ file: string }>({ path: '/config' })).resolves.toEqual({
      data: { file: 'config.yaml' },
      etag: '"r1"',
    })
  })

  it('accepts a 204 response without a body', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.resolve(new Response(null, { status: 204 }))),
    )
    await expect(api<unknown>({ path: '/auth/logout', method: 'POST' })).resolves.toBeUndefined()
  })

  it('maps the error envelope to ApiError', async () => {
    respond(
      409,
      { error: { code: 'source_busy', message: 'busy', details: { run: 4 } } },
      { 'X-Request-ID': 'req-7', 'Retry-After': '3' },
    )
    const error = await api<unknown>({ path: '/sources/a/runs', method: 'POST' }).catch(
      (e: unknown) => e,
    )
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({
      status: 409,
      code: 'source_busy',
      message: 'busy',
      requestId: 'req-7',
      details: { run: 4 },
      retryAfterMs: 3000,
    })
  })

  it('announces an expired session on 401', async () => {
    const expired = vi.fn()
    window.addEventListener('session-expired', expired)
    respond(401, { error: { code: 'unauthenticated', message: 'login required' } })
    await expect(api<unknown>({ path: '/config' })).rejects.toBeInstanceOf(ApiError)
    expect(expired).toHaveBeenCalledOnce()
    window.removeEventListener('session-expired', expired)
  })

  it('keeps an aborted request as AbortError', async () => {
    const controller = new AbortController()
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new DOMException('aborted', 'AbortError'))),
    )
    controller.abort()
    const error = await api<unknown>({ path: '/config', signal: controller.signal }).catch(
      (e: unknown) => e,
    )
    expect(error).toBeInstanceOf(DOMException)
    expect(error instanceof DOMException ? error.name : undefined).toBe('AbortError')
  })
})

describe('api retries', () => {
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  function json(status: number, body: unknown, headers: Record<string, string> = {}) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { 'Content-Type': 'application/json', ...headers },
    })
  }

  it('retries a read after a network failure and a 503, then succeeds', async () => {
    vi.useFakeTimers()
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError('Failed to fetch'))
      .mockResolvedValueOnce(json(503, { error: { code: 'unavailable', message: 'busy' } }))
      .mockResolvedValueOnce(json(200, { ok: true }))
    vi.stubGlobal('fetch', fetchMock)
    const result = api<{ ok: boolean }>({ path: '/config' })
    await vi.advanceTimersByTimeAsync(2_000)
    await expect(result).resolves.toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('gives up after two retries with network_unavailable', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn(() => Promise.reject(new TypeError('Failed to fetch')))
    vi.stubGlobal('fetch', fetchMock)
    const result = api<unknown>({ path: '/config' }).catch((error: unknown) => error)
    await vi.advanceTimersByTimeAsync(5_000)
    await expect(result).resolves.toMatchObject({ status: 0, code: 'network_unavailable' })
    expect(fetchMock).toHaveBeenCalledTimes(3)
  })

  it('does not retry writes or a Retry-After over ten seconds', async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        json(503, { error: { code: 'unavailable', message: 'busy' } }, { 'Retry-After': '30' }),
      ),
    )
    vi.stubGlobal('fetch', fetchMock)
    await expect(
      api<unknown>({ path: '/sources', method: 'POST', body: {} }),
    ).rejects.toMatchObject({
      status: 503,
    })
    await expect(api<unknown>({ path: '/config' })).rejects.toMatchObject({ status: 503 })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('maps a timed-out request to the timeout code', async () => {
    vi.useFakeTimers()
    vi.spyOn(AbortSignal, 'timeout').mockReturnValue(
      AbortSignal.abort(new DOMException('timed out', 'TimeoutError')),
    )
    vi.stubGlobal(
      'fetch',
      vi.fn(() => Promise.reject(new DOMException('timed out', 'TimeoutError'))),
    )
    const result = api<unknown>({ path: '/config' }).catch((error: unknown) => error)
    await vi.advanceTimersByTimeAsync(5_000)
    await expect(result).resolves.toMatchObject({ status: 0, code: 'timeout' })
  })
})

it('navigation cancels page requests while session requests continue', async () => {
  const { cancelPageRequests } = await import('./api')
  const signals: AbortSignal[] = []
  let finish: (() => void) | undefined
  vi.stubGlobal(
    'fetch',
    vi.fn(
      (_input: RequestInfo | URL, options?: RequestInit) =>
        new Promise<Response>((resolve, reject) => {
          const signal = options?.signal
          if (!signal) throw new Error('missing cancellation signal')
          signals.push(signal)
          signal.addEventListener(
            'abort',
            () => {
              reject(new DOMException('aborted', 'AbortError'))
            },
            { once: true },
          )
          finish = () => {
            resolve(
              new Response('{"authenticated":true}', {
                headers: { 'Content-Type': 'application/json' },
              }),
            )
          }
        }),
    ),
  )
  const page = api<unknown>({ path: '/runs' }).catch((error: unknown) => error)
  const session = api<{ authenticated: boolean }>({ path: '/auth/session', scope: 'session' })
  cancelPageRequests()
  expect(signals[0]?.aborted).toBe(true)
  expect(signals[1]?.aborted).toBe(false)
  await expect(page).resolves.toMatchObject({ name: 'AbortError' })
  finish?.()
  await expect(session).resolves.toEqual({ authenticated: true })
})
