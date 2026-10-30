<script lang="ts">
const dialogs: symbol[] = []
</script>
<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, nextTick } from 'vue'
import { X } from '@lucide/vue'
const {
  title,
  wide,
  role = 'dialog',
  hideHeader,
  descriptionId = '',
} = defineProps<{
  title: string
  wide?: boolean
  role?: 'dialog' | 'alertdialog'
  hideHeader?: boolean
  descriptionId?: string
}>()
const identity = Symbol('dialog')
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement>()
let previous: HTMLElement | null = null
let previousOverflow = ''
const key = (event: KeyboardEvent) => {
  if (dialogs.at(-1) !== identity) return
  if (event.key === 'Escape') {
    event.preventDefault()
    event.stopPropagation()
    emit('close')
  }
  if (event.key === 'Tab' && panel.value) {
    const list = Array.from(
      panel.value.querySelectorAll<HTMLElement>(
        'button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),a[href],summary',
      ),
    ).filter((element) => element.getClientRects().length > 0)
    const first = list[0],
      last = list[list.length - 1]
    if (
      event.shiftKey &&
      (document.activeElement === first || document.activeElement === panel.value)
    ) {
      event.preventDefault()
      last?.focus()
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault()
      first?.focus()
    }
  }
}
onMounted(() => {
  previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
  previousOverflow = document.body.style.overflow
  dialogs.push(identity)
  document.addEventListener('keydown', key)
  document.body.style.overflow = 'hidden'
  void nextTick(() => {
    const initial = panel.value?.querySelector<HTMLElement>('[data-dialog-focus]') ?? panel.value
    initial?.focus()
  })
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', key)
  const index = dialogs.indexOf(identity)
  if (index >= 0) dialogs.splice(index, 1)
  document.body.style.overflow = previousOverflow
  if (previous?.isConnected) previous.focus()
})
</script>
<template>
  <div
    class="modal-backdrop"
    @click.self="emit('close')"
  >
    <section
      ref="panel"
      tabindex="-1"
      :role="role"
      :aria-describedby="descriptionId || undefined"
      aria-modal="true"
      :aria-label="title"
      class="modal"
      :class="{ wide }"
    >
      <slot
        v-if="!hideHeader"
        name="header"
        ><header>
          <div>
            <h2>{{ title }}</h2>
          </div>
          <button
            class="icon-button"
            aria-label="关闭"
            @click="emit('close')"
          >
            <X :size="19" />
          </button></header
      ></slot>
      <slot />
    </section>
  </div>
</template>

<style scoped>
.modal-backdrop {
  position: fixed;
  inset: 0;
  z-index: 100;
  background: var(--scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-32px);
}
.modal {
  width: 600px;
  max-width: 100%;
  max-height: calc(100vh - 64px);
  overflow: auto;
  background: var(--raised);
  border: 1px solid var(--line);
  border-radius: var(--radius-14px);
  box-shadow: var(--shadow);
  outline: none;
}
.modal.wide {
  width: 820px;
}
.modal > :deep(header) {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-16px);
  padding: var(--space-20px) var(--space-22px) var(--space-20px) var(--space-28px);
  border-bottom: 1px solid var(--line);
  position: sticky;
  top: 0;
  background: var(--raised);
  z-index: 2;
}
.modal :deep(h2) {
  font-size: var(--font-17px);
  font-weight: 650;
}
@media (max-width: 900px) {
  .modal-backdrop {
    padding: var(--space-16px);
  }
}
@media (max-width: 900px) {
  .modal {
    max-height: calc(100vh - 32px);
  }
}
@media (max-width: 600px) {
  .modal-backdrop {
    padding: var(--space-8px);
  }
}
@media (max-width: 600px) {
  .modal {
    max-height: calc(100vh - 16px);
  }
}
@media (max-width: 600px) {
  .modal > :deep(header) {
    padding: var(--space-16px) var(--space-12px) var(--space-16px) var(--space-20px);
  }
}
</style>
