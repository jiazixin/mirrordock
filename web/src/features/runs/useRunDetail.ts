import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../../app/api'
import type { Run } from '../../app/types'

export function useRunDetail(id: () => number) {
  const run = ref<Run>()
  const attempt = ref(1)
  const text = ref('')
  const error = ref('')
  const detailError = ref('')
  const loading = ref(false)
  const logLoading = ref(false)
  const logOpen = ref(false)
  const followLatest = ref(true)
  // Paused and interrupted tasks can resume, so keep checking their status.
  const finished = computed(() =>
    ['succeeded', 'failed', 'timed_out', 'cancelled'].includes(run.value?.state ?? ''),
  )
  let alive = true
  let detailController: AbortController | undefined
  let logController: AbortController | undefined
  let detailTimer: ReturnType<typeof setTimeout> | undefined
  let logTimer: ReturnType<typeof setTimeout> | undefined
  let offset = 0
  let backlog = false
  let finalReadPending = false

  function stopDetail() {
    clearTimeout(detailTimer)
    detailController?.abort()
    detailController = undefined
    loading.value = false
  }
  function stopLog() {
    clearTimeout(logTimer)
    logController?.abort()
    logController = undefined
    logLoading.value = false
  }
  function canReadLog() {
    return (
      alive &&
      logOpen.value &&
      !!run.value?.attempts.length &&
      document.visibilityState === 'visible'
    )
  }
  async function refreshDetail() {
    if (!alive || loading.value || document.visibilityState !== 'visible') return
    clearTimeout(detailTimer)
    const controller = new AbortController()
    detailController = controller
    loading.value = true
    try {
      const result = await api<Run>(`/runs/${id()}`, 'GET', undefined, controller.signal)
      if (controller.signal.aborted) return
      const previous = run.value?.attempts.at(-1)?.number
      if (previous === undefined || attempt.value === previous)
        attempt.value = result.attempts.at(-1)?.number ?? 1
      run.value = result
      detailError.value = ''
    } catch (e) {
      if (!controller.signal.aborted) detailError.value = e instanceof Error ? e.message : String(e)
    } finally {
      if (detailController === controller) {
        loading.value = false
        if (alive && !finished.value && document.visibilityState === 'visible')
          detailTimer = setTimeout(() => void refreshDetail(), 3000)
      }
    }
  }
  async function refreshLog() {
    if (!canReadLog()) return
    if (logLoading.value) {
      // A terminal response may arrive while a log request is in flight. Read
      // once more afterwards rather than mistaking that earlier EOF for final.
      finalReadPending = true
      return
    }
    clearTimeout(logTimer)
    const controller = new AbortController()
    logController = controller
    logLoading.value = true
    finalReadPending = false
    let failed = false
    try {
      // Bound each turn to 2 MiB. A terminal task with a longer backlog continues
      // on later turns until EOF; only the last 2 MiB of text is retained.
      for (let page = 0; page < 16; page++) {
        const payload = await api<{ text: string; next_offset: number }>(
          `/runs/${id()}/logs?attempt=${attempt.value}&offset=${offset}`,
          'GET',
          undefined,
          controller.signal,
        )
        if (controller.signal.aborted) return
        const consumed = payload.next_offset - offset
        if (consumed === 0 && payload.text) text.value = payload.text
        else text.value = (text.value + payload.text).slice(-(2 << 20))
        offset = payload.next_offset
        backlog = consumed >= 128 << 10
        if (!backlog) break
      }
      error.value = ''
    } catch (e) {
      failed = true
      if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : String(e)
    } finally {
      if (logController === controller) {
        logLoading.value = false
        if (canReadLog() && (finalReadPending || (!failed && backlog) || !finished.value))
          logTimer = setTimeout(() => void refreshLog(), 3000)
      }
    }
  }
  watch(
    id,
    () => {
      stopDetail()
      stopLog()
      run.value = undefined
      attempt.value = 1
      text.value = ''
      error.value = ''
      detailError.value = ''
      offset = 0
      backlog = false
      finalReadPending = false
      followLatest.value = true
      logOpen.value = false
      void refreshDetail()
    },
    { immediate: true },
  )
  watch([() => run.value?.id, () => !!run.value?.attempts.length, attempt], () => {
    stopLog()
    offset = 0
    backlog = false
    finalReadPending = false
    text.value = ''
    error.value = ''
    void refreshLog()
  })
  watch(logOpen, (open) => {
    if (open) void refreshLog()
    else stopLog()
  })
  watch(
    () => run.value?.state,
    (value, previous) => {
      if (previous !== undefined && value !== previous) void refreshLog()
    },
  )
  function visibilityChanged() {
    if (document.visibilityState !== 'visible') {
      stopDetail()
      stopLog()
    } else {
      // Revalidate once on returning, including possible log cleanup elsewhere.
      void refreshDetail()
      void refreshLog()
    }
  }
  document.addEventListener('visibilitychange', visibilityChanged)
  onBeforeUnmount(() => {
    alive = false
    stopDetail()
    stopLog()
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return {
    run,
    attempt,
    text,
    error,
    detailError,
    loading,
    logLoading,
    logOpen,
    followLatest,
    finished,
    refreshDetail,
    refreshLog,
  }
}
