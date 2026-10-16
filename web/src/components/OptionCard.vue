<script setup lang="ts">
defineProps<{ label: string; description: string; disabled?: boolean }>()
const enabled = defineModel<boolean | null>({ required: true })
</script>

<template>
  <label
    class="source-option-card"
    :class="{ disabled }"
  >
    <span class="option-copy"
      ><strong>{{ label }}</strong
      ><small>{{ description }}</small></span
    >
    <input
      v-model="enabled"
      type="checkbox"
      role="switch"
      :aria-label="label"
      :disabled="disabled"
    />
  </label>
</template>

<style scoped>
.source-option-card {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-width: 0;
  min-height: 64px;
  padding: 12px 14px;
  border: 1px solid var(--line);
  border-radius: 4px;
  background: transparent;
  cursor: pointer;
  transition: border-color 0.15s;
}
.source-option-card:not(.disabled):hover {
  border-color: var(--line-strong);
}
.source-option-card.disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.source-option-card input:disabled {
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
  font-size: 13px;
  font-weight: 400;
  color: var(--t1);
  line-height: 1.5;
}
.option-copy small {
  font-size: 12px;
  line-height: 1.6;
  color: var(--t3);
}
.source-option-card input {
  appearance: none;
  position: relative;
  flex-shrink: 0;
  width: 32px;
  height: 18px;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 9px;
  background: var(--line-strong);
  cursor: pointer;
  transition: background 0.15s;
}
.source-option-card input:before {
  content: '';
  position: absolute;
  top: 2px;
  left: 2px;
  width: 14px;
  height: 14px;
  border-radius: 50%;
  background: var(--bg);
  transition: transform 0.15s;
}
.source-option-card input:checked {
  background: var(--acc);
}
.source-option-card input:checked:before {
  transform: translateX(14px);
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
