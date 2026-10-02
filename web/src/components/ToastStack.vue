<script setup lang="ts">
import { watch, onBeforeUnmount } from 'vue'
import { CircleCheck, CircleAlert, Info, X } from '@lucide/vue'

const props = withDefaults(
  defineProps<{
    items: readonly { id: number; message: string; tone: 'success' | 'error' | 'info' }[]
    duration?: number
  }>(),
  { duration: 5000 },
)
const emit = defineEmits<{ dismiss: [id: number] }>()
const timers = new Map<number, ReturnType<typeof setTimeout>>()
watch(
  () => props.items.map((item) => item.id),
  (ids) => {
    for (const [id, timer] of timers) {
      if (!ids.includes(id)) {
        clearTimeout(timer)
        timers.delete(id)
      }
    }
    for (const id of ids) {
      if (!timers.has(id))
        timers.set(
          id,
          setTimeout(() => emit('dismiss', id), props.duration),
        )
    }
  },
  { immediate: true, flush: 'post' },
)
onBeforeUnmount(() => {
  for (const timer of timers.values()) clearTimeout(timer)
  timers.clear()
})
</script>

<template>
  <Teleport to="body">
    <TransitionGroup
      name="toast"
      tag="div"
      class="toast-stack"
      aria-label="操作提示"
    >
      <div
        v-for="item in items"
        :key="item.id"
        class="toast-card"
        :class="item.tone"
        :style="{ '--toast-duration': `${duration}ms` }"
      >
        <span
          class="toast-icon"
          aria-hidden="true"
          ><component
            :is="item.tone === 'error' ? CircleAlert : item.tone === 'info' ? Info : CircleCheck"
            :size="20"
        /></span>
        <p
          class="toast-message"
          :role="item.tone === 'error' ? 'alert' : 'status'"
          aria-atomic="true"
        >
          {{ item.message }}
        </p>
        <button
          type="button"
          class="toast-close"
          aria-label="关闭提示"
          @click="emit('dismiss', item.id)"
        >
          <X :size="16" />
        </button>
        <div
          class="toast-track"
          aria-hidden="true"
        >
          <span class="toast-countdown"></span>
        </div>
      </div>
    </TransitionGroup>
  </Teleport>
</template>

<style scoped>
.toast-stack {
  position: fixed;
  z-index: 200;
  top: max(112px, env(safe-area-inset-top));
  right: max(24px, env(safe-area-inset-right));
  width: min(380px, calc(100vw - 32px));
  display: flex;
  flex-direction: column;
  gap: 12px;
  pointer-events: none;
}
.toast-card {
  --toast-color: #238569;
  --toast-tint: #edf8f3;
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 18px 16px 21px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: var(--paper, white);
  box-shadow:
    0 8px 28px #1d263b14,
    0 2px 6px #1d263b06;
  overflow: hidden;
  pointer-events: none;
}
.toast-card.error {
  --toast-color: #bd5366;
  --toast-tint: #fff0f2;
}
.toast-card.info {
  --toast-color: var(--blue);
  --toast-tint: var(--blue-light);
}
.toast-icon {
  display: grid;
  place-items: center;
  flex: 0 0 30px;
  height: 30px;
  border-radius: 8px;
  background: var(--toast-tint);
  color: var(--toast-color);
}
.toast-message {
  flex: 1;
  min-width: 0;
  margin: 4px 0 0;
  color: var(--dark);
  font-size: 13px;
  font-weight: 500;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.toast-close {
  pointer-events: auto;
  display: grid;
  place-items: center;
  flex: 0 0 26px;
  height: 26px;
  border: 0;
  border-radius: 5px;
  padding: 0;
  background: transparent;
  color: var(--muted);
  cursor: pointer;
}
.toast-close:hover {
  background: var(--bg, #f9fbfd);
  color: var(--dark);
}
.toast-close:focus-visible {
  outline: 2px solid var(--blue);
  outline-offset: 2px;
}
.toast-track {
  position: absolute;
  inset: auto 0 0;
  height: 3px;
  background: var(--toast-tint);
}
.toast-countdown {
  display: block;
  height: 100%;
  background: var(--toast-color);
  transform-origin: left;
  animation: toast-countdown var(--toast-duration) linear forwards;
}
@keyframes toast-countdown {
  from {
    transform: scaleX(1);
  }
  to {
    transform: scaleX(0);
  }
}
.toast-enter-active,
.toast-leave-active,
.toast-move {
  transition:
    transform 0.22s ease,
    opacity 0.22s ease;
}
.toast-enter-from,
.toast-leave-to {
  transform: translateX(32px);
  opacity: 0;
}
.toast-leave-active {
  position: absolute;
  width: 100%;
}
@media (max-width: 600px) {
  .toast-stack {
    top: max(16px, env(safe-area-inset-top));
    right: max(16px, env(safe-area-inset-right));
    gap: 10px;
  }
}
@media (prefers-reduced-motion: reduce) {
  .toast-enter-active,
  .toast-leave-active,
  .toast-move {
    transition: none;
  }
  /* Keep the remaining-time indicator despite the global decorative-animation reset. */
  .toast-countdown {
    animation: toast-countdown var(--toast-duration) linear forwards !important;
  }
}
@media (forced-colors: active) {
  .toast-card {
    border-color: CanvasText;
  }
  .toast-countdown {
    background: Highlight;
  }
}
</style>
