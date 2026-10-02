<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ChevronFirst, ChevronLast, ChevronLeft, ChevronRight } from '@lucide/vue'
import SelectField from './SelectField.vue'

const props = defineProps<{ total: number; count: number; noun?: string; disabled?: boolean }>()
const page = defineModel<number>('page', { required: true })
const pageSize = defineModel<number>('pageSize', { required: true })
const pages = computed(() => Math.max(1, Math.ceil(props.total / pageSize.value)))
const target = ref(String(page.value))
watch(page, (value) => {
  target.value = String(value)
})
const validTarget = computed(
  () =>
    /^\d+$/.test(target.value) && Number(target.value) >= 1 && Number(target.value) <= pages.value,
)
function go(value: number) {
  if (!props.disabled) page.value = Math.max(1, Math.min(pages.value, value))
}
function resize(value: number) {
  page.value = 1
  pageSize.value = value
}
</script>

<template>
  <nav
    class="pagination"
    aria-label="列表分页"
  >
    <span
      class="pagination-summary"
      role="status"
      >共 {{ total }} {{ noun ?? '条记录' }} · 第 {{ page }} / {{ pages }} 页 · 当前页
      {{ count }} 条</span
    >
    <div class="pagination-controls">
      <SelectField
        :model-value="pageSize"
        aria-label="每页条数"
        :disabled="disabled"
        :options="[20, 50, 100].map((value) => ({ value, label: `${value} 条 / 页` }))"
        @update:model-value="resize"
      />
      <div class="pagination-buttons">
        <button
          class="icon-button"
          type="button"
          aria-label="首页"
          :disabled="disabled || page <= 1"
          @click="go(1)"
        >
          <ChevronFirst :size="17" />
        </button>
        <button
          class="icon-button"
          type="button"
          aria-label="上一页"
          :disabled="disabled || page <= 1"
          @click="go(page - 1)"
        >
          <ChevronLeft :size="17" />
        </button>
        <span class="pagination-position"
          ><strong>{{ page }}</strong> / {{ pages }} 页</span
        >
        <button
          class="icon-button"
          type="button"
          aria-label="下一页"
          :disabled="disabled || page >= pages"
          @click="go(page + 1)"
        >
          <ChevronRight :size="17" />
        </button>
        <button
          class="icon-button"
          type="button"
          aria-label="末页"
          :disabled="disabled || page >= pages"
          @click="go(pages)"
        >
          <ChevronLast :size="17" />
        </button>
      </div>
      <form
        class="pagination-jump"
        @submit.prevent="validTarget && go(Number(target))"
      >
        <span>跳至</span
        ><input
          v-model="target"
          type="text"
          inputmode="numeric"
          aria-label="跳转页码"
          :disabled="disabled"
        /><span>页</span>
        <button
          class="button compact"
          type="submit"
          :disabled="disabled || !validTarget"
        >
          跳转
        </button>
      </form>
    </div>
  </nav>
</template>
