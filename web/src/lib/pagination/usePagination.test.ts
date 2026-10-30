import { describe, expect, it } from 'vitest'
import { effectScope, ref } from 'vue'
import { usePagination } from './usePagination'

describe('usePagination', () => {
  it('resets filters, clamps shrinking data, and releases watchers on disposal', () => {
    const scope = effectScope()
    const items = ref(Array.from({ length: 120 }, (_, i) => i))
    const filter = ref('')
    const pagination = scope.run(() => usePagination(items, [filter]))
    if (!pagination) throw new Error('scope did not run')
    pagination.page.value = 3
    expect(pagination.visible.value).toHaveLength(20)
    filter.value = 'changed'
    expect(pagination.page.value).toBe(1)
    pagination.page.value = 2
    items.value = [1, 2]
    expect(pagination.page.value).toBe(1)
    scope.stop()
    pagination.page.value = 2
    filter.value = 'disposed'
    expect(pagination.page.value).toBe(2)
  })
})
