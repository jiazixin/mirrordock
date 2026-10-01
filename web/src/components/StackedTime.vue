<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ value: string; datetime?: string }>()
const parts = computed(() => props.value.split(/\s+/))
</script>

<template>
  <time v-if="value !== '—'" class="stacked-time" :datetime="datetime" :title="value">
    <strong>{{ parts[0] }}</strong>
    <small>{{ parts[1] }}</small>
  </time>
  <span v-else>—</span>
</template>
