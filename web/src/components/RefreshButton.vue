<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RefreshCw } from '@lucide/vue'

// The icon spins while the clicked action (or an external busy state) is pending. It always
// completes at least one turn and stops on a turn boundary, so fast refreshes still read as feedback.
const props = withDefaults(
  defineProps<{ action: () => unknown; spinning?: boolean; size?: number }>(),
  {
    spinning: false,
    size: 16,
  },
)
// Every click runs the action, as a plain button would; a retry can supersede a stalled request.
const pending = ref(0)
const turning = ref(false)
const busy = computed(() => pending.value > 0 || props.spinning)
const reducedMotion = () => window.matchMedia('(prefers-reduced-motion: reduce)').matches
watch(
  busy,
  (value) => {
    if (value) turning.value = true
    // Without animation no iteration event arrives to end the turn.
    else if (reducedMotion()) turning.value = false
  },
  { immediate: true },
)
function iteration() {
  if (!busy.value) turning.value = false
}
async function run() {
  pending.value++
  try {
    await props.action()
  } finally {
    pending.value--
  }
}
</script>

<template>
  <button
    type="button"
    :aria-busy="busy"
    @click="run"
  >
    <span
      class="refresh-icon"
      :class="{ turning }"
      aria-hidden="true"
      @animationiteration="iteration"
      ><RefreshCw :size="size" /></span
    ><slot />
  </button>
</template>

<style scoped>
.refresh-icon {
  display: inline-flex;
}
.refresh-icon.turning {
  animation: refresh-turn 0.7s linear infinite;
}
@keyframes refresh-turn {
  to {
    transform: rotate(360deg);
  }
}
</style>
