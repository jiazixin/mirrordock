<script setup lang="ts" generic="T extends string | number">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useAttrs, useId, watch } from 'vue'
import { Check, ChevronDown } from '@lucide/vue'

defineOptions({ inheritAttrs: false })
const props = defineProps<{
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
const position = ref({ left: '0px', top: '0px', width: '0px', maxHeight: '280px' })
const selected = computed(() => props.options.find((option) => option.value === model.value))
let typed = ''
let typedAt = 0

function place() {
  if (!trigger.value) return
  const rect = trigger.value.getBoundingClientRect()
  const width = Math.min(rect.width, window.innerWidth - 16)
  const below = window.innerHeight - rect.bottom - 12
  const above = rect.top - 12
  const height = Math.min(props.options.length * 42 + 12 || 54, 280)
  const upwards = below < height && above > below
  const maxHeight = Math.max(42, Math.min(height, upwards ? above : below))
  position.value = {
    left: `${Math.max(8, Math.min(rect.left, window.innerWidth - width - 8))}px`,
    top: `${upwards ? Math.max(8, rect.top - maxHeight - 6) : rect.bottom + 6}px`,
    width: `${width}px`,
    maxHeight: `${maxHeight}px`,
  }
}

async function reveal() {
  if (props.disabled) return
  active.value = props.options.findIndex(
    (option) => option.value === model.value && !option.disabled,
  )
  if (active.value < 0) active.value = props.options.findIndex((option) => !option.disabled)
  place()
  open.value = true
  await nextTick()
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
  const option = props.options[index]
  if (!option || option.disabled) return
  model.value = option.value
  close(true)
}

function move(step: number) {
  const length = props.options.length
  for (let i = 1; i <= length; i++) {
    const index = (active.value + step * i + length) % length
    if (!props.options[index]?.disabled) {
      active.value = index
      break
    }
  }
  nextTick(scrollActive)
}

async function key(event: KeyboardEvent) {
  if (props.disabled) return
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
    const index = props.options.findIndex(
      (option) => !option.disabled && option.label.toLocaleLowerCase().startsWith(typed),
    )
    if (index >= 0) {
      active.value = index
      nextTick(scrollActive)
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
  () => props.disabled,
  (value) => {
    if (value) close()
  },
)
watch(
  () => props.options,
  (options, before) => {
    if (
      open.value &&
      (options.length !== before.length ||
        options.some(
          (option, index) =>
            option.value !== before[index]?.value ||
            option.label !== before[index]?.label ||
            option.disabled !== before[index]?.disabled,
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
</script>

<template>
  <div
    class="select-field"
    :class="{ 'is-open': open }"
  >
    <button
      ref="trigger"
      v-bind="attrs"
      :id="String(attrs.id ?? id)"
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
          :class="{
            selected: option.value === model,
            active: index === active,
            disabled: option.disabled,
          }"
          @pointermove="!option.disabled && (active = index)"
          @click="choose(index)"
        >
          <span>{{ option.label }}</span
          ><Check
            v-if="option.value === model"
            :size="16"
            aria-hidden="true"
          />
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
