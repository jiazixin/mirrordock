<script setup lang="ts">
defineProps<{ label: string; description: string; disabled?: boolean }>()
const enabled = defineModel<boolean | null>({ required: true })
</script>

<template>
  <label
    class="source-option-card"
    :class="{ disabled, on: !!enabled }"
  >
    <span class="option-copy"
      ><strong>{{ label }}</strong
      ><small>{{ description }}</small></span
    >
    <span class="option-control"
      ><span
        class="option-state"
        aria-hidden="true"
        >{{ enabled ? 'ON' : 'OFF' }}</span
      ><input
        v-model="enabled"
        type="checkbox"
        role="switch"
        :aria-label="label"
        :disabled="disabled"
    /></span>
  </label>
</template>

<style scoped>
/* A labelled row, not a stacked tile: the global label rule is column-based. */
.source-option-card {
  display: flex;
  flex-direction: row;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
  min-height: 60px;
  padding: 12px 16px;
  border: 1px solid var(--line);
  border-radius: 10px;
  background: transparent;
  color: var(--t1);
  cursor: pointer;
  transition:
    border-color 0.15s,
    background 0.15s;
}
.source-option-card:not(.disabled):hover {
  border-color: var(--line-strong);
  background: var(--hover);
}
.source-option-card.on {
  border-color: var(--ok-bd);
  background: var(--tint);
}
.source-option-card.disabled {
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
.option-control {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  flex-shrink: 0;
}
.option-state {
  min-width: 3ch;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 11px;
  line-height: 1;
  letter-spacing: 0.04em;
  text-align: right;
  color: var(--t3);
}
.on .option-state {
  color: var(--ok);
}
.source-option-card input {
  appearance: none;
  position: relative;
  flex-shrink: 0;
  width: 36px;
  height: 20px;
  margin: 0;
  padding: 0;
  border: 1px solid var(--line-strong);
  border-radius: 999px;
  background: var(--gray-bg);
  cursor: pointer;
  transition:
    background 0.15s,
    border-color 0.15s;
}
.source-option-card input:before {
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
.source-option-card:not(.disabled):hover input:not(:checked) {
  border-color: var(--t3);
}
.source-option-card input:checked {
  border-color: var(--brand);
  background: var(--brand);
}
.source-option-card input:checked:before {
  transform: translateX(16px);
}
.source-option-card input:disabled {
  opacity: 1;
  cursor: not-allowed;
}
.source-option-card:has(input:focus-visible) {
  outline: 2px solid var(--focus);
  outline-offset: 2px;
}
.source-option-card input:focus-visible {
  outline: none;
}
@media (forced-colors: active) {
  .source-option-card input {
    appearance: auto;
    border: 0;
    background: none;
  }
  .source-option-card input:before {
    display: none;
  }
}
@media (prefers-reduced-motion: reduce) {
  .source-option-card,
  .source-option-card input,
  .source-option-card input:before {
    transition: none;
  }
}
</style>
