<script setup lang="ts">
import SelectField from '../../components/SelectField.vue'
import { ref, watch } from 'vue'
import { ArrowDownToLine, Download, RefreshCw } from '@lucide/vue'
import Modal from '../../components/Modal.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import { api } from '../../app/api'
import type { Run } from '../../app/types'
import { time, duration, bytes, phases } from '../../app/format'
const props = defineProps<{ id: number }>()
const emit = defineEmits<{ close: [] }>()
const run = ref<Run>()
const attempt = ref(1)
const text = ref('')
const error = ref('')
const detailError = ref('')
const logView = ref<HTMLElement>()
const logLoading = ref(false)
const followLatest = ref(true)

watch(
  [text, followLatest],
  () => {
    if (followLatest.value && logView.value) logView.value.scrollTop = logView.value.scrollHeight
  },
  { flush: 'post' },
)
let refreshLog = () => Promise.resolve()

watch(
  () => props.id,
  async (id, _, onCleanup) => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setInterval> | undefined
    let loading = false
    onCleanup(() => {
      controller.abort()
      clearInterval(timer)
    })
    run.value = undefined
    followLatest.value = true
    text.value = ''
    error.value = ''
    detailError.value = ''
    const load = async () => {
      if (loading || controller.signal.aborted) return
      loading = true
      try {
        const result = await api<Run>(`/runs/${id}`, 'GET', undefined, controller.signal)
        if (controller.signal.aborted) return
        const previous = run.value?.attempts.at(-1)?.number
        // Follow new attempts only when the user was viewing the latest attempt.
        if (previous === undefined || attempt.value === previous)
          attempt.value = result.attempts.at(-1)?.number ?? 1
        run.value = result
        detailError.value = ''
      } catch (e) {
        if (!controller.signal.aborted)
          detailError.value = e instanceof Error ? e.message : String(e)
      } finally {
        loading = false
      }
    }
    await load()
    if (!controller.signal.aborted)
      timer = setInterval(() => {
        void load()
      }, 3000)
  },
  { immediate: true },
)

watch(
  [() => run.value?.id, () => !!run.value?.attempts.length, attempt],
  ([id, hasAttempts, number], _, onCleanup) => {
    text.value = ''
    error.value = ''
    logLoading.value = false
    if (!id || !hasAttempts) return
    const controller = new AbortController()
    let offset = 0
    let loading = false
    const loadLog = async () => {
      if (loading || controller.signal.aborted) return
      loading = true
      logLoading.value = true
      try {
        // Bound each refresh to 2 MiB; continue any backlog on the next tick.
        for (let page = 0; page < 16; page++) {
          const payload = await api<{ text: string; next_offset: number }>(
            `/runs/${id}/logs?attempt=${number}&offset=${offset}`,
            'GET',
            undefined,
            controller.signal,
          )
          if (controller.signal.aborted) return
          const consumed = payload.next_offset - offset
          if (consumed === 0 && payload.text) text.value = payload.text
          else text.value = (text.value + payload.text).slice(-(2 << 20))
          offset = payload.next_offset
          if (consumed < 128 << 10) break
        }
        error.value = ''
      } catch (e) {
        if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : String(e)
      } finally {
        loading = false
        if (!controller.signal.aborted) logLoading.value = false
      }
    }
    refreshLog = loadLog
    void loadLog()
    const timer = window.setInterval(() => {
      void loadLog()
    }, 3000)
    onCleanup(() => {
      window.clearInterval(timer)
      controller.abort()
      refreshLog = () => Promise.resolve()
    })
  },
  { immediate: true },
)

function download() {
  const link = document.createElement('a')
  link.href = `/api/runs/${props.id}/logs?attempt=${attempt.value}&download=1`
  link.download = `run-${props.id}-attempt-${attempt.value}.log`
  link.click()
}
</script>
<template>
  <Modal
    :title="`运行详情 · #${id}`"
    wide
    @close="emit('close')"
    ><div
      v-if="run"
      class="detail-body"
    >
      <div class="detail-title">
        <h3>{{ run.name }}</h3>
        <StatusBadge :state="run.state" />
      </div>
      <div class="detail-grid">
        <div>
          <small>运行来源</small
          ><strong>{{ run.trigger === 'schedule' ? '自动计划' : '手动触发' }}</strong>
        </div>
        <div>
          <small>执行耗时 / 总预算</small
          ><strong>{{ duration(run.elapsed_seconds) }} / {{ duration(run.budget_seconds) }}</strong>
        </div>
        <div>
          <small>阶段</small><strong>{{ phases[run.progress.phase] ?? run.progress.phase }}</strong>
        </div>
        <div>
          <small>传输字节</small><strong>{{ bytes(run.progress.bytes) }}</strong>
        </div>
        <div>
          <small>开始时间</small><strong>{{ time(run.started_at) }}</strong>
        </div>
        <div>
          <small>结束时间</small><strong>{{ time(run.ended_at) }}</strong>
        </div>
      </div>
      <div
        v-if="run.error"
        class="alert error"
      >
        <div>
          <strong>{{ run.error_code }}</strong>
          <p>{{ run.error }}</p>
        </div>
      </div>
      <h3 class="section-heading">来源尝试</h3>
      <div
        v-for="item in run.attempts"
        :key="item.number"
        class="attempt-row"
      >
        <strong>#{{ item.number }}</strong>
        <div>
          <code>{{ item.candidate }}</code
          ><small>{{ time(item.started_at) }} → {{ time(item.ended_at) }}</small
          ><span
            v-if="item.error_code"
            class="error-text"
            >{{ item.error_code }} · {{ item.error }}</span
          >
        </div>
        <span class="muted">{{
          item.log_cleared
            ? '日志已清理'
            : item.error_code
              ? '失败'
              : item.ended_at
                ? '完成'
                : '进行中'
        }}</span>
      </div>
      <div class="log-toolbar">
        <h3>原始日志 <small>最多保留当前 2 MiB 文本</small></h3>
        <SelectField
          v-model="attempt"
          aria-label="选择日志尝试"
          :options="
            run.attempts.map((item) => ({ value: item.number, label: `尝试 #${item.number}` }))
          "
        /><button
          class="icon-button log-refresh"
          :class="{ 'is-loading': logLoading }"
          aria-label="刷新日志"
          :aria-busy="logLoading"
          :disabled="logLoading || !run.attempts.length"
          title="刷新日志"
          @click="refreshLog()"
        >
          <RefreshCw :size="16" /></button
        ><button
          class="icon-button log-follow"
          aria-label="持续跟随最新日志"
          :aria-pressed="followLatest"
          :title="followLatest ? '正在跟随最新日志，点击暂停' : '跟随最新日志'"
          @click="followLatest = !followLatest"
        >
          <ArrowDownToLine :size="16" /></button
        ><button
          class="button compact"
          :disabled="
            !run.attempts.length ||
            run.attempts.find((item) => item.number === attempt)?.log_cleared
          "
          @click="download"
        >
          <Download :size="15" />下载
        </button>
      </div>
      <pre
        ref="logView"
        class="log-view"
        aria-label="原始日志"
        >{{ text || '暂无日志。' }}</pre
      >
      <details class="snapshot">
        <summary>配置快照与事件</summary>
        <pre>{{ JSON.stringify(run.snapshot, null, 2) }}</pre>
        <p
          v-for="(event, index) in run.events"
          :key="index"
        >
          {{ time(event.at) }} · {{ event.message }}
        </p>
      </details>
    </div>
    <div
      v-if="detailError || error"
      class="alert error"
      role="alert"
    >
      {{ detailError || error }}
    </div></Modal
  >
</template>
