import { errorText } from '../../app/errors'
import { ref, watch, onScopeDispose, type Ref } from 'vue'
import { api } from '../../app/api'
import { state } from '../../app/state'
import type { Run } from '../../app/types'

export function useRunPage(filters: {
  state: Ref<string>
  source: Ref<string>
  age: Ref<string>
  trigger?: Ref<string>
}) {
  const page = ref(1),
    pageSize = ref(50),
    total = ref(0),
    records = ref<Run[]>([]),
    loading = ref(false),
    loadError = ref('')
  let revision = 0
  let controller: AbortController | undefined
  async function load() {
    controller?.abort()
    const current = new AbortController()
    controller = current
    const request = ++revision
    loading.value = true
    loadError.value = ''
    const query = new URLSearchParams({
      state: filters.state.value,
      source: filters.source.value,
      limit: String(pageSize.value),
      offset: String((page.value - 1) * pageSize.value),
    })
    if (filters.age.value) query.set('range', filters.age.value)
    if (filters.trigger?.value) query.set('trigger', filters.trigger.value)
    try {
      const payload = await api<{ items: Run[]; total: number }>({
        path: `/runs?${query}`,
        signal: current.signal,
      })
      if (request !== revision) return
      total.value = payload.total
      const last = Math.max(1, Math.ceil(payload.total / pageSize.value))
      if (page.value > last) {
        page.value = last
        return
      }
      records.value = payload.items
    } catch (error) {
      if (request === revision && !current.signal.aborted) loadError.value = errorText(error)
    } finally {
      if (request === revision) loading.value = false
    }
  }
  watch(
    [...Object.values(filters), pageSize],
    () => {
      page.value = 1
    },
    { flush: 'sync' },
  )
  watch([page, pageSize, ...Object.values(filters)], () => {
    void load()
  })
  watch(
    () => state.revision,
    () => {
      void load()
    },
    { immediate: true },
  )
  onScopeDispose(() => {
    revision++
    controller?.abort()
  })
  return { page, pageSize, total, records, loading, loadError, load }
}
