<script setup lang="ts">
defineProps<{ kind: 'rsync' | 'reposync'; size?: number }>()
</script>
<template>
  <svg
    :width="size ?? 19"
    :height="size ?? 19"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.7"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <template v-if="kind === 'rsync'">
      <path d="M3 7h17m-4-4 4 4-4 4M21 17H4m4-4-4 4 4 4" />
      <path d="M7 5v4m5 6v4" />
    </template>
    <template v-else>
      <path d="m3 6 7-3 7 3-7 3-7-3Z M3 6v8l7 3V9m7-3v5" />
      <path d="M14 21v-4m4 4v-7m4 7V11" />
    </template>
  </svg>
</template>
