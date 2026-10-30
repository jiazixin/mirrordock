<script setup lang="ts">
import { computed } from 'vue'
import { errorText } from '../app/errors'
import { ApiError } from '../app/api'
const { error, field } = defineProps<{ error: unknown; field: string }>()
const message = computed(() => {
  if (!(error instanceof ApiError) || error.code !== 'invalid_argument') return ''
  const details = error.details
  if (
    typeof details !== 'object' ||
    details === null ||
    !('fields' in details) ||
    !Array.isArray(details.fields)
  )
    return ''
  const match: unknown = details.fields.find((item: unknown) => {
    if (
      typeof item !== 'object' ||
      item === null ||
      !('path' in item) ||
      typeof item.path !== 'string'
    )
      return false
    const path = item.path.replace(/^sources\.\d+\./, '')
    return path === field || path.startsWith(field + '.')
  })
  if (!match) return ''
  return errorText(
    new ApiError(error.message, {
      status: error.status,
      code: error.code,
      requestId: error.requestId,
      details: { fields: [match] },
    }),
  )
})
</script>
<template>
  <span
    v-if="message"
    class="field-issue"
    role="alert"
    >{{ message }}</span
  >
</template>
<style scoped>
.field-issue {
  color: var(--red);
  font-size: var(--font-12px);
  display: block;
}
</style>
