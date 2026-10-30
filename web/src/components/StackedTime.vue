<script setup lang="ts">
import { computed } from 'vue'

const { value, datetime = '' } = defineProps<{ value: string; datetime?: string }>()
const parts = computed(() => value.split(/\s+/))
</script>

<template>
  <time
    v-if="value !== '—'"
    class="stacked-time"
    :datetime="datetime || undefined"
    :title="value"
  >
    <strong>{{ parts[0] }}</strong>
    <small>{{ parts[1] }}</small>
  </time>
  <span v-else>—</span>
</template>

<style scoped>
.stacked-time {
  display: inline-flex;
  flex-direction: column;
  gap: var(--space-3px);
  white-space: nowrap;
}
.stacked-time :deep(strong) {
  font-weight: 600;
  color: var(--t1);
}
.stacked-time :deep(small) {
  margin-top: 0;
  color: var(--t2);
}
</style>
