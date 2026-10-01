<script setup lang="ts">
defineProps<{ label: string; description: string }>()
const enabled = defineModel<boolean | null>({ required: true })
</script>

<template>
  <label class="source-option-card">
    <span class="option-copy"><strong>{{ label }}</strong><small>{{ description }}</small></span>
    <input v-model="enabled" type="checkbox" role="switch" :aria-label="label" />
  </label>
</template>

<style scoped>
.source-option-card{position:relative;display:flex;flex-direction:column;gap:0;min-width:0;min-height:94px;padding:14px 14px 38px;border:1px solid var(--line);border-radius:8px;background:#fafbfc;cursor:pointer;transition:border-color .15s,background .15s}
.source-option-card:hover{border-color:#c4d2fa;background:#f5f7fc}
.option-copy{display:flex;flex-direction:column;gap:4px;overflow-wrap:anywhere}
.option-copy strong{font-size:13px;font-weight:500;color:#424a56;line-height:1.5}
.option-copy small{font-size:12px;line-height:1.6;color:var(--muted)}
.source-option-card input{appearance:none;position:absolute;right:14px;bottom:12px;width:34px;height:20px;margin:0;padding:0;border:0;border-radius:20px;background:#dfe5ef;cursor:pointer;transition:background .15s}
.source-option-card input:before{content:"";position:absolute;top:2px;left:2px;width:16px;height:16px;border-radius:50%;background:#fff;box-shadow:0 1px 3px #20305022;transition:transform .15s}
.source-option-card input:checked{background:var(--blue)}
.source-option-card input:checked:before{transform:translateX(14px)}
.source-option-card:has(input:focus-visible){outline:3px solid #9bb4ff;outline-offset:2px}
.source-option-card input:focus-visible{outline:none}
@media(forced-colors:active){.source-option-card input{appearance:auto;background:none}.source-option-card input:before{display:none}}
@media(prefers-reduced-motion:reduce){.source-option-card,.source-option-card input,.source-option-card input:before{transition:none}}
</style>
