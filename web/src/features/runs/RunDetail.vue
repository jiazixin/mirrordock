<script setup lang="ts">
import SelectField from '../../components/SelectField.vue'
import { computed, ref, watch } from 'vue'
import { ArrowDownToLine, Download, RefreshCw } from '@lucide/vue'
import Modal from '../../components/Modal.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import StackedTime from '../../components/StackedTime.vue'
import { time, duration, bytes, phases } from '../../app/format'
import { useRunDetail } from './useRunDetail'
import './run-detail.css'
const props = defineProps<{ id: number }>()
const emit = defineEmits<{ close: [] }>()
const {
  run,
  attempt,
  text,
  error,
  detailError,
  loading,
  logLoading,
  logOpen,
  followLatest,
  finished,
  refreshDetail,
  refreshLog,
} = useRunDetail(() => props.id)
const logView = ref<HTMLElement>()
const steps = ['准备', '传输', '校验', '发布']
const step = computed(() => {
  if (run.value?.state === 'succeeded') return 4
  if (
    !run.value?.started_at ||
    ['waiting_space', 'waiting_downloads'].includes(run.value.progress.phase)
  )
    return -1
  return (
    (
      { prepare: 0, scan: 0, transfer: 1, verify: 2, publish: 3, complete: 3 } as Record<
        string,
        number
      >
    )[run.value.progress.phase] ?? -1
  )
})
const percent = computed(() => {
  const value = run.value?.progress.percent
  return value == null ? null : Math.min(100, Math.max(0, value))
})
watch(
  [text, followLatest, logOpen],
  () => {
    if (followLatest.value && logView.value) logView.value.scrollTop = logView.value.scrollHeight
  },
  { flush: 'post' },
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
      class="detail-body run-detail"
    >
      <div class="detail-title">
        <div>
          <h3>{{ run.name }}</h3>
          <p>
            {{ run.trigger === 'schedule' ? '自动计划' : '手动触发' }} ·
            {{ run.type === 'github_release' ? 'GitHub Release' : run.type }}
          </p>
        </div>
        <div class="detail-heading-actions">
          <StatusBadge :state="run.state" /><button
            class="icon-button"
            aria-label="刷新运行详情"
            :disabled="loading"
            @click="refreshDetail"
          >
            <RefreshCw :size="16" />
          </button>
        </div>
      </div>
      <ol
        class="run-lifecycle"
        aria-label="同步阶段"
      >
        <li
          v-for="(label, index) in steps"
          :key="label"
          :class="{ reached: index < step, current: index === step }"
          :aria-current="index === step ? 'step' : undefined"
        >
          <span class="run-stage-node">{{ String(index + 1).padStart(2, '0') }}</span
          ><strong>{{ label }}</strong
          ><span
            v-if="index < step"
            class="visually-hidden"
            >已完成</span
          >
        </li>
      </ol>
      <div class="run-detail-stats">
        <article class="run-stat-primary">
          <span>实际接收 <small v-if="run.progress.received_incomplete">部分统计</small></span
          ><strong>{{
            run.progress.received_bytes == null ? '未统计' : bytes(run.progress.received_bytes)
          }}</strong>
        </article>
        <article>
          <span>处理数据量</span
          ><strong>{{ run.type === 'reposync' ? '未统计' : bytes(run.progress.bytes) }}</strong>
        </article>
        <article>
          <span>执行耗时</span><strong>{{ duration(run.elapsed_seconds) }}</strong>
        </article>
        <article>
          <span>来源尝试</span><strong>{{ run.attempts.length }}<small>次</small></strong>
        </article>
      </div>
      <div class="run-phase">
        <span
          >{{ phases[run.progress.phase] ?? run.progress.phase
          }}<small>总预算 {{ duration(run.budget_seconds) }}</small></span
        ><strong v-if="percent !== null && run.progress.phase === 'transfer'"
          >{{ percent.toFixed(1) }}%</strong
        >
      </div>
      <div
        v-if="percent !== null && run.progress.phase === 'transfer'"
        class="run-progress"
        role="progressbar"
        aria-label="传输进度"
        :aria-valuenow="percent"
        :aria-valuemin="0"
        :aria-valuemax="100"
      >
        <span :style="{ width: `${percent}%` }" />
      </div>
      <div class="run-detail-times">
        <div>
          <small>提交时间</small
          ><StackedTime
            :value="time(run.created_at)"
            :datetime="run.created_at"
          />
        </div>
        <div>
          <small>开始时间</small
          ><StackedTime
            :value="time(run.started_at)"
            :datetime="run.started_at"
          />
        </div>
        <div>
          <small>结束时间</small
          ><StackedTime
            :value="time(run.ended_at)"
            :datetime="run.ended_at"
          />
        </div>
      </div>
      <p class="run-detail-note">
        处理数据量可能包含复用文件，不等于网络接收量。{{
          finished ? '任务已结束，状态自动刷新已停止。' : '状态每 3 秒更新；切到后台后暂停读取。'
        }}
      </p>
      <div
        v-if="run.error"
        class="alert error"
      >
        <div>
          <strong>{{ run.error_code }}</strong>
          <p>{{ run.error }}</p>
        </div>
      </div>
      <details
        class="run-detail-section"
        open
      >
        <summary>
          来源尝试 <span>{{ run.attempts.length }} 次</span>
        </summary>
        <div class="run-section-content">
          <p
            v-if="!run.attempts.length"
            class="muted"
          >
            任务尚未开始尝试来源。
          </p>
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
        </div>
      </details>
      <details
        class="run-detail-section"
        :open="logOpen"
        @toggle="logOpen = ($event.target as HTMLDetailsElement).open"
      >
        <summary>
          原始日志 <span>{{ logOpen ? '收起以停止读取' : '展开后读取' }}</span>
        </summary>
        <div
          v-show="logOpen"
          class="run-section-content"
        >
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
        </div>
      </details>
      <details class="snapshot run-detail-section">
        <summary>配置快照与事件</summary>
        <div class="run-section-content">
          <pre>{{ JSON.stringify(run.snapshot, null, 2) }}</pre>
          <p
            v-for="(event, index) in run.events"
            :key="index"
          >
            {{ time(event.at) }} · {{ event.message }}
          </p>
        </div>
      </details>
    </div>
    <p
      v-else-if="!detailError"
      class="run-detail-loading"
      role="status"
    >
      正在读取运行详情…
    </p>
    <div
      v-if="detailError || (logOpen && error)"
      class="alert error"
      role="alert"
    >
      {{ detailError || error }}
      <button
        class="button compact"
        @click="detailError ? refreshDetail() : refreshLog()"
      >
        重试
      </button>
    </div></Modal
  >
</template>
