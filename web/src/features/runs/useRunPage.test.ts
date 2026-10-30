import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import type * as ApiModule from '../../app/api'
import { api, ApiError } from '../../app/api'
import { useRunPage } from './useRunPage'
vi.mock('../../app/api', async (original) => ({
  ...(await original<typeof ApiModule>()),
  api: vi.fn(),
}))

afterEach(() => vi.resetAllMocks())

describe('useRunPage', () => {
  it('retains successful data on failure and cancels requests when disposed', async () => {
    const request = vi.mocked(api)
    request.mockResolvedValue({ items: [], total: 7 })
    const scope = effectScope()
    const page = scope.run(() => useRunPage({ state: ref(''), source: ref(''), age: ref('') }))
    if (!page) throw new Error('scope did not run')
    await nextTick()
    expect(page.total.value).toBe(7)
    request.mockRejectedValue(
      new ApiError('failed', { status: 500, code: 'internal', requestId: 'fixture' }),
    )
    await page.load()
    expect(page.total.value).toBe(7)
    expect(page.loadError.value).not.toBe('')
    expect(page.loading.value).toBe(false)
    const signal = request.mock.calls.at(-1)?.[0]?.signal
    scope.stop()
    expect(signal?.aborted).toBe(true)
  })

  it('rejects a stale response after filters change', async () => {
    let resolveOld: ((value: unknown) => void) | undefined
    const request = vi.mocked(api)
    request.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        }),
    )
    request.mockResolvedValue({ items: [], total: 2 })
    const source = ref('first')
    const scope = effectScope()
    const page = scope.run(() => useRunPage({ state: ref(''), source, age: ref('') }))
    if (!page) throw new Error('scope did not run')
    source.value = 'second'
    await nextTick()
    await nextTick()
    resolveOld?.({ items: [], total: 99 })
    await nextTick()
    expect(page.total.value).toBe(2)
    expect(request.mock.calls[0]?.[0]?.signal?.aborted).toBe(true)
    scope.stop()
  })
})
