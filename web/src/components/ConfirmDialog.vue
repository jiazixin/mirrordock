<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { CircleHelp, CircleAlert, TriangleAlert } from '@lucide/vue'

const props = withDefaults(
  defineProps<{
    title: string
    message: string
    confirmLabel: string
    cancelLabel?: string
    tone?: 'primary' | 'amber' | 'red'
  }>(),
  { cancelLabel: '取消', tone: 'primary' },
)
const emit = defineEmits<{ settle: [accepted: boolean] }>()
const id = useId()
const cancelButton = ref<HTMLButtonElement>()
const confirmButton = ref<HTMLButtonElement>()
const icon = computed(() =>
  props.tone === 'red' ? TriangleAlert : props.tone === 'amber' ? CircleAlert : CircleHelp,
)
let previous: HTMLElement | null = null
let previousOverflow = ''

// Capture phase so Escape/Tab never reach an underlying Modal or the shell menu.
function key(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('settle', false)
  } else if (event.key === 'Tab') {
    event.preventDefault()
    event.stopPropagation()
    const next = document.activeElement === cancelButton.value ? confirmButton : cancelButton
    next.value?.focus()
  }
}
onMounted(() => {
  previous = document.activeElement as HTMLElement | null
  previousOverflow = document.body.style.overflow
  document.body.style.overflow = 'hidden'
  document.addEventListener('keydown', key, true)
  // Destructive actions start on the safe choice; others accept with Enter.
  ;(props.tone === 'red' ? cancelButton : confirmButton).value?.focus()
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', key, true)
  document.body.style.overflow = previousOverflow
  previous?.focus()
})
</script>

<template>
  <Teleport to="body"
    ><div
      class="confirm-backdrop"
      @click.self="emit('settle', false)"
    >
      <section
        role="alertdialog"
        aria-modal="true"
        :aria-labelledby="`${id}-title`"
        :aria-describedby="`${id}-message`"
        class="confirm-dialog"
        :class="tone"
      >
        <span
          class="confirm-icon"
          aria-hidden="true"
          ><component
            :is="icon"
            :size="22"
        /></span>
        <div class="confirm-body">
          <h2 :id="`${id}-title`">{{ title }}</h2>
          <p :id="`${id}-message`">{{ message }}</p>
        </div>
        <footer class="confirm-actions">
          <button
            ref="cancelButton"
            type="button"
            class="button"
            @click="emit('settle', false)"
          >
            {{ cancelLabel }}</button
          ><button
            ref="confirmButton"
            type="button"
            class="button"
            :class="tone === 'primary' ? 'primary' : `tone-${tone}`"
            @click="emit('settle', true)"
          >
            {{ confirmLabel }}
          </button>
        </footer>
      </section>
    </div></Teleport
  >
</template>

<style scoped>
.confirm-backdrop {
  position: fixed;
  inset: 0;
  z-index: 150;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 32px;
  background: var(--scrim);
  animation: confirm-fade 0.16s ease-out;
}
.confirm-dialog {
  --confirm-color: var(--ok);
  --confirm-soft: var(--ok-bg);
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 4px 16px;
  width: 440px;
  max-width: 100%;
  max-height: calc(100vh - 64px);
  overflow: auto;
  padding: 26px 26px 22px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--raised);
  box-shadow: var(--shadow);
  animation: confirm-rise 0.18s ease-out;
}
.confirm-dialog.amber {
  --confirm-color: var(--amb);
  --confirm-soft: var(--amb-soft);
}
.confirm-dialog.red {
  --confirm-color: var(--red);
  --confirm-soft: var(--red-soft);
}
.confirm-icon {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 50%;
  background: var(--confirm-soft);
  color: var(--confirm-color);
}
.confirm-body {
  min-width: 0;
  padding-top: 2px;
}
.confirm-body h2 {
  margin: 0;
  color: var(--t1);
  font-size: 17px;
  font-weight: 650;
  line-height: 1.5;
}
.confirm-body p {
  margin: 6px 0 0;
  color: var(--t2);
  font-size: 14px;
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.confirm-actions {
  grid-column: 1 / -1;
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  margin-top: 22px;
}
.confirm-actions .button {
  min-width: 88px;
}
.confirm-actions .button:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
@keyframes confirm-fade {
  from {
    opacity: 0;
  }
}
@keyframes confirm-rise {
  from {
    opacity: 0;
    transform: translateY(8px) scale(0.98);
  }
}
@media (max-width: 600px) {
  .confirm-backdrop {
    padding: 16px;
  }
  .confirm-dialog {
    padding: 22px 20px 18px;
    gap: 4px 14px;
  }
  .confirm-actions .button {
    flex: 1;
  }
}
@media (forced-colors: active) {
  .confirm-dialog {
    border-color: CanvasText;
  }
}
</style>
