import { afterEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick } from 'vue'
import type * as ApiModule from '../../app/api'
import { api } from '../../app/api'
import { useRunDetail } from './useRunDetail'

vi.mock('../../app/api', async (original) => ({
  ...(await original<typeof ApiModule>()),
  api: vi.fn(),
}))
afterEach(() => {
  vi.resetAllMocks()
  vi.useRealTimers()
})

describe('useRunDetail', () => {
  it('cancels a pending detail and removes polling and visibility subscriptions on disposal', async () => {
    vi.useFakeTimers()
    vi.mocked(api).mockReturnValue(new Promise(() => {}))
    const scope = effectScope()
    scope.run(() => useRunDetail(() => 7))
    const signal = vi.mocked(api).mock.calls[0]?.[0].signal
    expect(signal?.aborted).toBe(false)
    scope.stop()
    expect(signal?.aborted).toBe(true)
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(10_000)
    await nextTick()
    expect(api).toHaveBeenCalledOnce()
  })
})
