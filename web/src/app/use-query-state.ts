import { onScopeDispose, watch, type Ref } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { notify } from './state'
import { errorText } from './errors'

type QueryValue = string | number | undefined
/** Persists shareable page state and restores it on reload and browser navigation. */
export function useQueryState(
  fields: Record<string, Ref<QueryValue>>,
  allowed: Record<string, readonly QueryValue[]> = {},
) {
  const route = useRoute()
  const router = useRouter()
  const defaults = Object.fromEntries(
    Object.entries(fields).map(([key, value]) => [key, value.value]),
  )
  let pagePath = route.path
  const view = route.meta.view
  watch(
    () => route.path,
    (path) => {
      if (view && route.meta.view === view) pagePath = path
    },
    { flush: 'sync' },
  )
  let disposed = false
  let restoring = false
  function restore() {
    if (route.path !== pagePath) return
    restoring = true
    for (const [key, field] of Object.entries(fields).sort(([a], [b]) => {
      const priority = (key: string) =>
        key.toLowerCase().endsWith('pagesize') ? 0 : Number(key.toLowerCase().endsWith('page')) + 1
      return priority(a) - priority(b)
    })) {
      const value = route.query[key]
      const fallback = defaults[key]
      if (typeof value !== 'string' || value.length > 1024) field.value = fallback
      else if (typeof fallback === 'string')
        field.value = !allowed[key] || allowed[key].includes(value) ? value : fallback
      else {
        const number = Number(value)
        field.value =
          Number.isSafeInteger(number) &&
          number > 0 &&
          (!key.toLowerCase().endsWith('pagesize') || [20, 50, 100].includes(number))
            ? number
            : fallback
      }
    }
    restoring = false
  }
  restore()
  watch(() => route.query, restore, { flush: 'sync' })
  watch(Object.values(fields), () => {
    if (disposed || route.path !== pagePath || restoring) return
    const query: LocationQueryRaw = { ...route.query }
    for (const [key, field] of Object.entries(fields)) {
      if (field.value === defaults[key] || field.value === undefined || field.value === '')
        query[key] = undefined
      else query[key] = String(field.value)
    }
    if (router.resolve({ path: pagePath, query }).fullPath === route.fullPath) return
    void router.replace({ path: pagePath, query }).catch((error: unknown) => {
      if (!disposed) notify(errorText(error), 'error')
    })
  })
  onScopeDispose(() => {
    disposed = true
  })
}
