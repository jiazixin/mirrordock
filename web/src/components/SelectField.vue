<script setup lang="ts" generic="T extends string | number">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useAttrs, useId, watch } from 'vue'
import { ChevronDown } from '@lucide/vue'

defineOptions({ inheritAttrs: false })
const { options, disabled } = defineProps<{
  options: readonly { value: T; label: string; disabled?: boolean }[]
  disabled?: boolean
}>()
const model = defineModel<T>({ required: true })
const attrs = useAttrs()
const id = useId()
const trigger = ref<HTMLButtonElement>()
const menu = ref<HTMLElement>()
const open = ref(false)
const active = ref(-1)
const position = ref({ left: '0px', top: '0px', minWidth: '0px', maxHeight: '280px' })
const selected = computed(() => options.find((option) => option.value === model.value))
let typed = ''
let typedAt = 0

function place() {
  if (!trigger.value) return
  const rect = trigger.value.getBoundingClientRect()
  // The menu is at least as wide as the trigger and grows to fit its labels.
  const width = Math.min(Math.max(rect.width, menu.value?.offsetWidth ?? 0), window.innerWidth - 16)
  const below = window.innerHeight - rect.bottom - 12
  const above = rect.top - 12
  const height = Math.min(options.length * 42 + 12 || 54, 280)
  const upwards = below < height && above > below
  const maxHeight = Math.max(42, Math.min(height, upwards ? above : below))
  position.value = {
    left: `${Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))}px`,
    top: `${upwards ? Math.max(8, rect.top - maxHeight - 6) : rect.bottom + 6}px`,
    minWidth: `${Math.min(rect.width, window.innerWidth - 16)}px`,
    maxHeight: `${maxHeight}px`,
  }
}

async function reveal() {
  if (disabled) return
  active.value = options.findIndex((option) => option.value === model.value && !option.disabled)
  if (active.value < 0) active.value = options.findIndex((option) => !option.disabled)
  place()
  open.value = true
  await nextTick()
  place()
  scrollActive()
}

function close(focus = false) {
  open.value = false
  typed = ''
  if (focus) trigger.value?.focus()
}

function scrollActive() {
  const option = menu.value?.querySelector<HTMLElement>(`[data-index="${active.value}"]`)
  if (option && menu.value) {
    const top = option.offsetTop
    const bottom = top + option.offsetHeight
    if (top < menu.value.scrollTop) menu.value.scrollTop = top
    else if (bottom > menu.value.scrollTop + menu.value.clientHeight)
      menu.value.scrollTop = bottom - menu.value.clientHeight
  }
}

function choose(index: number) {
  const option = options[index]
  if (!option || option.disabled) return
  model.value = option.value
  close(true)
}

function move(step: number) {
  const length = options.length
  for (let distance = 1; distance <= length; distance++) {
    const index = (active.value + step * distance + length) % length
    if (!options[index]?.disabled) {
      active.value = index
      break
    }
  }
  void nextTick(scrollActive)
}

async function key(event: KeyboardEvent) {
  if (disabled) return
  if (event.key === 'Tab') {
    close()
    return
  }
  if (event.key === 'Escape') {
    if (open.value) {
      event.preventDefault()
      event.stopPropagation()
      close(true)
    }
    return
  }
  if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' '].includes(event.key)) {
    event.preventDefault()
    if (!open.value) {
      await reveal()
      if (event.key !== 'Home' && event.key !== 'End') return
    }
    if (event.key === 'Enter' || event.key === ' ') {
      choose(active.value)
      return
    }
    if (event.key === 'Home' || event.key === 'End') {
      active.value = event.key === 'Home' ? -1 : 0
      move(event.key === 'Home' ? 1 : -1)
    } else move(event.key === 'ArrowDown' ? 1 : -1)
    return
  }
  if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
    event.preventDefault()
    if (!open.value) await reveal()
    typed = (Date.now() - typedAt > 700 ? '' : typed) + event.key.toLocaleLowerCase()
    typedAt = Date.now()
    const index = options.findIndex(
      (option) => !option.disabled && option.label.toLocaleLowerCase().startsWith(typed),
    )
    if (index >= 0) {
      active.value = index
      void nextTick(scrollActive)
    }
  }
}

function outside(event: PointerEvent) {
  const target = event.target as Node | null
  if (open.value && target && !trigger.value?.contains(target) && !menu.value?.contains(target))
    close()
}
function scroll(event: Event) {
  if (!open.value || (event.target instanceof Node && menu.value?.contains(event.target))) return
  const rect = trigger.value?.getBoundingClientRect()
  if (!rect || rect.bottom <= 0 || rect.top >= window.innerHeight) close()
  else place()
}
watch(
  () => disabled,
  (value) => {
    if (value) close()
  },
)
watch(
  () => options,
  (options, before) => {
    if (
      open.value &&
      (options.length !== before.length ||
        options.some(
          (option, index) =>
            option.value !== before[index]?.value ||
            option.label !== before[index].label ||
            option.disabled !== before[index].disabled,
        ))
    )
      close()
  },
)
onMounted(() => {
  document.addEventListener('pointerdown', outside, true)
  document.addEventListener('scroll', scroll, true)
  window.addEventListener('resize', place)
})
onBeforeUnmount(() => {
  document.removeEventListener('pointerdown', outside, true)
  document.removeEventListener('scroll', scroll, true)
  window.removeEventListener('resize', place)
})
function optionClasses(option: { value: T; disabled?: boolean }, index: number) {
  return {
    selected: option.value === model.value,
    active: index === active.value,
    disabled: option.disabled,
  }
}
</script>

<template>
  <div
    class="select-field"
    :class="{ 'is-open': open }"
  >
    <button
      v-bind="attrs"
      :id="String(attrs.id ?? id)"
      ref="trigger"
      type="button"
      class="select-trigger"
      role="combobox"
      aria-haspopup="listbox"
      :aria-expanded="open"
      :aria-controls="`${id}-list`"
      :aria-activedescendant="open && active >= 0 ? `${id}-option-${active}` : undefined"
      :disabled="disabled"
      @click="open ? close() : reveal()"
      @keydown="key"
      @blur="close()"
    >
      <span>{{ selected?.label ?? '请选择' }}</span
      ><ChevronDown
        :size="16"
        aria-hidden="true"
      />
    </button>
    <Teleport to="body">
      <ul
        v-if="open"
        :id="`${id}-list`"
        ref="menu"
        role="listbox"
        class="select-menu"
        :style="position"
        :aria-label="
          typeof attrs['aria-label'] === 'string' ? attrs['aria-label'] : selected?.label
        "
        @pointerdown.prevent
      >
        <li
          v-for="(option, index) in options"
          :id="`${id}-option-${index}`"
          :key="option.value"
          :data-index="index"
          role="option"
          :aria-selected="option.value === model"
          :aria-disabled="option.disabled || undefined"
          :class="optionClasses(option, index)"
          @pointermove="!option.disabled && (active = index)"
          @click="choose(index)"
        >
          <span>{{ option.label }}</span>
        </li>
        <li
          v-if="!options.length"
          class="select-empty"
        >
          暂无选项
        </li>
      </ul>
    </Teleport>
  </div>
</template>

<style scoped>
/* Zero specificity: the component's CSS chunk loads after global.css, so a plain class rule
   would override container widths such as .filter-toolbar .select-field. */
:where(.select-field) {
  position: relative;
  min-width: 0;
  width: 100%;
}
.select-trigger {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-10px);
  width: 100%;
  min-height: 42px;
  padding: var(--space-9px) var(--space-12px) var(--space-9px) var(--space-14px);
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-9px);
  background: var(--panel);
  color: var(--t1);
  text-align: left;
  font-size: var(--font-14px);
  font-weight: 400;
  line-height: 20px;
  transition: border-color var(--motion-0d15s);
}
.select-trigger > :deep(span) {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.select-trigger > :deep(svg) {
  flex-shrink: 0;
  color: var(--t3);
  transition: transform var(--motion-0d15s);
}
.select-trigger:hover:not(:disabled) {
  border-color: var(--line-hover);
}
.is-open > :deep(.select-trigger) {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-soft);
}
.is-open > :deep(.select-trigger > svg) {
  transform: rotate(180deg);
}
.select-menu {
  position: fixed;
  z-index: 300;
  list-style: none;
  margin: 0;
  padding: var(--space-5px);
  border: 1px solid var(--line);
  border-radius: var(--radius-11px);
  background: var(--raised);
  box-shadow: var(--shadow);
  width: max-content;
  max-width: min(calc(100vw - 16px), 420px);
  overflow-y: auto;
  overscroll-behavior: contain;
  scrollbar-width: thin;
  font-size: var(--font-13px);
  line-height: 1.5;
}
.select-menu > :deep(li) {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-10px);
  min-height: 36px;
  padding: var(--space-8px) var(--space-11px);
  border-radius: var(--radius-8px);
  color: var(--t1);
  cursor: pointer;
}
.select-menu > :deep(li > span) {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.select-menu > :deep(li.active) {
  background: var(--hover);
}
.select-menu > :deep(li.selected) {
  color: var(--brand);
  font-weight: 600;
  background: var(--brand-soft);
}
.select-menu > :deep(li.disabled) {
  color: var(--t3);
  cursor: not-allowed;
}
.select-menu > :deep(.select-empty) {
  color: var(--t3);
  cursor: default;
}
</style>
