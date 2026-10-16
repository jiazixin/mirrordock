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
            :size="18"
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
  top: calc(var(--header-height) + 16px);
  right: max(24px, env(safe-area-inset-right));
  width: min(380px, calc(100vw - 32px));
  display: flex;
  flex-direction: column;
  gap: 10px;
  pointer-events: none;
}
.toast-card {
  --toast-color: var(--acc);
  position: relative;
  display: flex;
  align-items: flex-start;
  gap: 12px;
  padding: 14px 12px 16px 16px;
  border: 1px solid var(--line-strong);
  border-radius: 6px;
  background: var(--raised);
  box-shadow: var(--shadow);
  overflow: hidden;
  pointer-events: none;
}
.toast-card.error {
  --toast-color: var(--red);
}
.toast-card.info {
  --toast-color: var(--t2);
}
.toast-icon {
  display: grid;
  place-items: center;
  flex: 0 0 20px;
  height: 22px;
  color: var(--toast-color);
}
.toast-message {
  flex: 1;
  min-width: 0;
  margin: 1px 0 0;
  color: var(--t1);
  font-size: 13px;
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.toast-close {
  pointer-events: auto;
  display: grid;
  place-items: center;
  flex: 0 0 24px;
  height: 24px;
  border: 0;
  border-radius: 4px;
  padding: 0;
  background: transparent;
  color: var(--t3);
  cursor: pointer;
}
.toast-close:hover {
  background: var(--hover);
  color: var(--t1);
}
.toast-close:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
.toast-track {
  position: absolute;
  inset: auto 0 0;
  height: 2px;
  background: transparent;
}
.toast-countdown {
  display: block;
  height: 100%;
  background: var(--toast-color);
  opacity: 0.7;
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
  transform: translateX(24px);
  opacity: 0;
}
.toast-leave-active {
  position: absolute;
  width: 100%;
}
@media (max-width: 600px) {
  .toast-stack {
    top: calc(var(--header-height) + 12px);
    right: max(16px, env(safe-area-inset-right));
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
