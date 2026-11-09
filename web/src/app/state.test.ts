import { afterEach, describe, expect, it, vi } from 'vitest'
import { runSource } from './state'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('runSource', () => {
  it('sends an Idempotency-Key outside a secure context', async () => {
    // Plain HTTP on a LAN address has no crypto.randomUUID.
    const { crypto } = globalThis
    vi.stubGlobal('crypto', { getRandomValues: crypto.getRandomValues.bind(crypto) })
    const fetch = vi.fn<typeof globalThis.fetch>(() =>
      Promise.resolve(
        new Response(JSON.stringify({ items: [] }), {
          headers: { 'Content-Type': 'application/json' },
        }),
      ),
    )
    vi.stubGlobal('fetch', fetch)

    await runSource('demo')

    const [path, init] = fetch.mock.calls[0] ?? []
    expect(path).toBe('/api/sources/demo/runs')
    expect(new Headers(init?.headers).get('Idempotency-Key')).toMatch(/^[0-9a-f]{32}$/)
  })
})
