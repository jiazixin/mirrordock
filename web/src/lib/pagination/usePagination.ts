import { computed, ref, watch, type Ref } from 'vue'

export function usePagination<T>(items: Ref<T[]>, filters: Ref<unknown>[] = []) {
  const page = ref(1)
  const pageSize = ref(50)
  watch(
    [...filters, pageSize],
    () => {
      page.value = 1
    },
    { flush: 'sync' },
  )
  watch(
    () => items.value.length,
    (total) => {
      page.value = Math.min(page.value, Math.max(1, Math.ceil(total / pageSize.value)))
    },
    { flush: 'sync' },
  )
  const visible = computed(() =>
    items.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value),
  )
  return { page, pageSize, visible }
}
