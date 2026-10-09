<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { X } from '@lucide/vue'
defineProps<{ title: string; wide?: boolean }>()
const emit = defineEmits<{ close: [] }>()
const panel = ref<HTMLElement>()
let previous: HTMLElement | null = null
let previousOverflow = ''
const key = (event: KeyboardEvent) => {
  if (event.key === 'Escape') emit('close')
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
  previous = document.activeElement as HTMLElement
  previousOverflow = document.body.style.overflow
  document.addEventListener('keydown', key)
  document.body.style.overflow = 'hidden'
  panel.value?.focus()
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', key)
  document.body.style.overflow = previousOverflow
  previous?.focus()
})
</script>
<template>
  <Teleport to="body"
    ><div
      class="modal-backdrop"
      @click.self="emit('close')"
    >
      <section
        ref="panel"
        tabindex="-1"
        role="dialog"
        aria-modal="true"
        :aria-label="title"
        class="modal"
        :class="{ wide }"
      >
        <header>
          <div>
            <span class="eyebrow">MIRRORDOCK</span>
            <h2>{{ title }}</h2>
          </div>
          <button
            class="icon-button"
            aria-label="关闭"
            @click="emit('close')"
          >
            <X :size="19" />
          </button>
        </header>
        <slot />
      </section></div
  ></Teleport>
</template>
