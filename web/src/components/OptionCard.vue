<script setup lang="ts">
defineProps<{ label: string; description: string; disabled?: boolean }>()
const enabled = defineModel<boolean | null>({ required: true })
</script>

<template>
  <label
    class="option-row"
    :class="{ disabled }"
  >
    <input
      v-model="enabled"
      type="checkbox"
      role="switch"
      :aria-label="label"
      :disabled="disabled"
    /><span class="option-copy"
      ><strong>{{ label }}</strong
      ><small>{{ description }}</small></span
    >
  </label>
</template>

<style scoped>
/* Switch first, then label and description; the global label rule is column-based. */
.option-row {
  display: flex;
  flex-direction: row;
  align-items: flex-start;
  gap: 12px;
  /* Borderless: keep the click target to the visible switch and text. */
  width: fit-content;
  max-width: 100%;
  min-width: 0;
  padding: 4px 0;
  color: var(--t1);
  cursor: pointer;
}
.option-row.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.option-copy {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  overflow-wrap: anywhere;
}
.option-copy strong {
  font-size: 14px;
  font-weight: 600;
  color: var(--t1);
  line-height: 1.5;
}
.option-copy small {
  font-size: 12px;
  line-height: 1.6;
  color: var(--t3);
}
.option-row input {
  appearance: none;
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  margin: 1px 0 0;
  padding: 0;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  background: var(--gray-bg);
  cursor: pointer;
  transition:
    background 0.15s,
    border-color 0.15s;
}
.option-row input:before {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--panel);
  box-shadow: 0 1px 2px rgba(18, 26, 21, 0.25);
  transition:
    transform 0.15s,
    background 0.15s;
}
.option-row:not(.disabled):hover input:not(:checked) {
  border-color: var(--t3);
}
.option-row input:checked {
  border-color: var(--brand);
  background: var(--brand);
}
.option-row input:checked:before {
  transform: translateX(16px);
}
.option-row input:disabled {
  opacity: 1;
  cursor: not-allowed;
}
.option-row input:focus-visible {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
@media (forced-colors: active) {
  .option-row input {
    appearance: auto;
    border: 0;
    background: none;
  }
  .option-row input:before {
    display: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .option-row input,
  .option-row input:before {
    transition: none;
  }
}
</style>
