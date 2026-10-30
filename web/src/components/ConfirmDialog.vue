<script setup lang="ts">
import { computed, useId } from 'vue'
import ModalDialog from './ModalDialog.vue'
import { CircleHelp, CircleAlert, TriangleAlert } from '@lucide/vue'

const {
  title,
  message,
  confirmLabel,
  cancelLabel = '取消',
  tone = 'primary',
} = defineProps<{
  title: string
  message: string
  confirmLabel: string
  cancelLabel?: string
  tone?: 'primary' | 'amber' | 'red'
}>()
const emit = defineEmits<{ settle: [accepted: boolean] }>()
const id = useId()
const icon = computed(() => ({ red: TriangleAlert, amber: CircleAlert, primary: CircleHelp })[tone])
</script>

<template>
  <ModalDialog
    :title="title"
    role="alertdialog"
    :hide-header="true"
    :description-id="`${id}-message`"
    class="confirm-overlay"
    :class="tone"
    @close="emit('settle', false)"
  >
    <div class="confirm-dialog">
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
          :data-dialog-focus="tone === 'red' ? '' : undefined"
          type="button"
          class="button"
          @click="emit('settle', false)"
        >
          {{ cancelLabel }}</button
        ><button
          :data-dialog-focus="tone !== 'red' ? '' : undefined"
          type="button"
          class="button"
          :class="tone === 'primary' ? 'primary' : `tone-${tone}`"
          @click="emit('settle', true)"
        >
          {{ confirmLabel }}
        </button>
      </footer>
    </div>
  </ModalDialog>
</template>

<style scoped>
.confirm-overlay {
  z-index: 150;
  --confirm-color: var(--ok);
  --confirm-soft: var(--ok-bg);
}
.confirm-overlay :deep(.modal) {
  width: 440px;
}
.confirm-dialog {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--space-4px) var(--space-16px);
  width: 440px;
  max-width: 100%;
  max-height: calc(100vh - 64px);
  overflow: auto;
  padding: var(--space-26px) var(--space-26px) var(--space-22px);
}
.confirm-overlay.amber {
  --confirm-color: var(--amb);
  --confirm-soft: var(--amb-soft);
}
.confirm-overlay.red {
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
  padding-top: var(--space-2px);
}
.confirm-body h2 {
  margin: 0;
  color: var(--t1);
  font-size: var(--font-17px);
  font-weight: 650;
  line-height: 1.5;
}
.confirm-body p {
  margin: var(--space-6px) 0 0;
  color: var(--t2);
  font-size: var(--font-14px);
  line-height: 1.7;
  overflow-wrap: anywhere;
}
.confirm-actions {
  grid-column: 1 / -1;
  display: flex;
  justify-content: flex-end;
  gap: var(--space-10px);
  margin-top: var(--space-22px);
}
.confirm-actions .button {
  min-width: 88px;
}
.confirm-actions .button:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
@media (max-width: 600px) {
  .confirm-overlay {
    padding: var(--space-16px);
  }
  .confirm-dialog {
    padding: var(--space-22px) var(--space-20px) var(--space-18px);
    gap: var(--space-4px) var(--space-14px);
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
