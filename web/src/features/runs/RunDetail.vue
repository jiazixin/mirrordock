<script setup lang="ts">
import type { Run } from '../../app/types'
import SelectField from '../../components/SelectField.vue'
import { computed, ref, watch } from 'vue'
import { ChevronsDown, Download } from '@lucide/vue'
import ModalDialog from '../../components/ModalDialog.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import StatusBadge from '../../components/StatusBadge.vue'
import StackedTime from '../../components/StackedTime.vue'
import { phases, sourceType, runStage, runStages } from '../../app/labels'
import { time } from '../../app/server-time'
import { formatSize } from '../../lib/format/bytes'
import { formatDuration } from '../../lib/format/duration'
import { eventText, runErrorText } from '../../app/error-text'
import { useRunDetail } from './useRunDetail'
import { formatPercent } from '../../lib/format/number'
const { id } = defineProps<{ id: number }>()
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
} = useRunDetail(() => id)
const logView = ref<HTMLElement>()
const steps = runStages
const step = computed(() => {
  if (run.value?.state === 'succeeded') return 4
  if (
    !run.value?.started_at ||
    ['waiting_space', 'waiting_downloads'].includes(run.value.progress.phase)
  )
    return -1
  return runStage(run.value.progress.phase)
})
const percent = computed(() => {
  const value = run.value?.progress.percent
  return typeof value !== 'number' ? null : Math.min(100, Math.max(0, value))
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
  link.href = `/api/runs/${id}/logs?attempt=${attempt.value}&download=true`
  link.download = `run-${id}-attempt-${attempt.value}.log`
  link.click()
}
function attemptLabel(item: Run['attempts'][number]) {
  if (item.log_cleared) return '日志已清理'
  if (item.error_code) return '失败'
  if (item.ended_at) return '完成'
  return '进行中'
}
const attemptOptions = computed(
  () =>
    run.value?.attempts.map((item) => ({ value: item.number, label: `尝试 #${item.number}` })) ??
    [],
)
const downloadDisabled = computed(
  () =>
    !run.value?.attempts.length ||
    run.value.attempts.find((item) => item.number === attempt.value)?.log_cleared,
)
const receivedLabel = computed(() => {
  const received = run.value?.progress.received_bytes
  return typeof received === 'number' ? formatSize(received) : '未统计'
})
</script>
<template>
  <ModalDialog
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
            {{ sourceType(run.type) }}
          </p>
        </div>
        <div class="detail-heading-actions">
          <StatusBadge :state="run.state" /><RefreshButton
            class="icon-button"
            aria-label="刷新运行详情"
            :disabled="loading"
            :action="refreshDetail"
          />
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
          ><strong>{{ receivedLabel }}</strong>
        </article>
        <article>
          <span>处理数据量</span
          ><strong>{{
            run.type === 'reposync' ? '未统计' : formatSize(run.progress.transferred_bytes)
          }}</strong>
        </article>
        <article>
          <span>执行耗时</span><strong>{{ formatDuration(run.elapsed_ms / 1000) }}</strong>
        </article>
        <article>
          <span>来源尝试</span><strong>{{ run.attempts.length }}<small>次</small></strong>
        </article>
      </div>
      <div class="run-phase">
        <span
          >{{ phases[run.progress.phase] ?? '未知阶段'
          }}<small>总预算 {{ formatDuration(run.budget_ms / 1000) }}</small></span
        ><strong v-if="percent !== null && run.progress.phase === 'transfer'">{{
          formatPercent(percent)
        }}</strong>
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
          <p>{{ runErrorText(run) }}</p>
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
                >{{ item.error_code }} · {{ runErrorText(item) }}</span
              >
            </div>
            <span class="muted">{{ attemptLabel(item) }}</span>
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
              :options="attemptOptions"
            />
            <div class="log-actions">
              <RefreshButton
                class="icon-button log-refresh"
                :class="{ 'is-loading': logLoading }"
                aria-label="刷新日志"
                :spinning="logLoading"
                :disabled="logLoading || !run.attempts.length"
                title="刷新日志"
                :action="() => refreshLog()"
              /><button
                class="icon-button log-follow"
                aria-label="持续跟随最新日志"
                :aria-pressed="followLatest"
                :title="followLatest ? '正在跟随最新日志，点击暂停' : '跟随最新日志'"
                @click="followLatest = !followLatest"
              >
                <ChevronsDown :size="16" /></button
              ><button
                class="button compact"
                :disabled="downloadDisabled"
                @click="download"
              >
                <Download :size="15" />下载
              </button>
            </div>
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
            v-for="event in run.events"
            :key="`${event.at}|${event.code}|${event.message}`"
          >
            {{ time(event.at) }} · {{ eventText(event) }}
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
    </div></ModalDialog
  >
</template>

<style scoped>
.detail-body {
  padding: var(--space-24px) var(--space-28px) var(--space-28px);
}
.detail-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--space-24px);
}
.attempt-row {
  display: grid;
  grid-template-columns: 40px minmax(0, 1fr) auto;
  gap: var(--space-14px);
  padding: var(--space-14px) 0;
  border-bottom: 1px solid var(--line);
  align-items: baseline;
  font-size: var(--font-13px);
}
.attempt-row > :deep(strong) {
  color: var(--t2);
  font-weight: 600;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: var(--font-12px);
}
.attempt-row > :deep(div) {
  min-width: 0;
}
.attempt-row :deep(code) {
  overflow-wrap: anywhere;
  color: var(--t1);
}
.attempt-row :deep(small) {
  display: block;
  margin: var(--space-4px) 0;
}
.log-toolbar {
  display: flex;
  gap: var(--space-6px);
  align-items: center;
  margin: var(--space-20px) 0 var(--space-12px);
}
.log-toolbar :deep(h3) {
  margin-right: auto;
}
.log-toolbar :deep(.select-field) {
  width: 130px;
}
.log-view {
  background: var(--head);
  border: 1px solid var(--line);
  color: var(--t2);
  border-radius: var(--radius-10px);
  padding: var(--space-14px) var(--space-16px);
  margin: 0;
  font-size: var(--font-12px);
  line-height: 1.8;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  max-height: 360px;
  overflow: auto;
}
.snapshot {
  font-size: var(--font-12px);
  color: var(--t2);
}
.snapshot :deep(pre) {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-size: var(--font-12px);
  background: var(--head);
  border: 1px solid var(--line);
  border-radius: var(--radius-10px);
  padding: var(--space-14px) var(--space-16px);
  color: var(--t2);
}
.snapshot :deep(p) {
  line-height: 1.8;
}
.log-refresh.is-loading {
  color: var(--acc);
  opacity: 1;
}
.log-follow[aria-pressed='true'] {
  color: var(--acc);
  border-color: var(--acc-soft);
  background: var(--acc-soft);
}
@media (max-width: 600px) {
  .detail-body {
    padding: var(--space-20px);
  }
}
@media (max-width: 600px) {
  .attempt-row {
    grid-template-columns: 32px minmax(0, 1fr);
  }
}
@media (max-width: 600px) {
  .attempt-row > :deep(.muted) {
    grid-column: 2;
  }
}
@media (max-width: 600px) {
  .log-toolbar {
    flex-wrap: wrap;
  }
}
@media (max-width: 600px) {
  .log-toolbar :deep(h3) {
    width: 100%;
    margin-bottom: var(--space-4px);
  }
}
.run-detail {
  min-width: 0;
}
.run-detail .detail-title {
  gap: var(--space-16px);
  align-items: flex-start;
  margin-bottom: var(--space-28px);
}
.run-detail .detail-title > div:first-child {
  min-width: 0;
}
.run-detail .detail-title h3 {
  font-size: var(--font-26px);
  font-weight: 400;
  letter-spacing: -0.02em;
  overflow-wrap: anywhere;
}
.run-detail .detail-title p {
  font-size: var(--font-13px);
  color: var(--t2);
  margin-top: var(--space-6px);
}
.detail-heading-actions {
  display: flex;
  align-items: center;
  gap: var(--space-10px);
  flex-shrink: 0;
}
/* Let the borderless refresh glyph, not its hit area, meet the content edge. */
.detail-heading-actions > .icon-button:last-child {
  margin-right: calc(-1 * var(--space-8px));
}
.run-lifecycle {
  list-style: none;
  padding: 0;
  margin: 0 0 var(--space-32px);
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: var(--space-4px);
}
.run-lifecycle li {
  display: flex;
  align-items: baseline;
  gap: var(--space-10px);
  padding-top: var(--space-12px);
  border-top: 6px solid var(--gray-bg);
  color: var(--t3);
  min-width: 0;
}
.run-stage-node {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: var(--font-12px);
}
.run-lifecycle strong {
  font-weight: 600;
  font-size: var(--font-14px);
}
.run-lifecycle .reached {
  border-top-color: var(--bar);
  color: var(--ok);
}
.run-lifecycle .reached .run-stage-node {
  color: var(--t3);
}
.run-lifecycle .current {
  border-top-color: var(--blue);
  color: var(--blue);
}
.run-lifecycle .current .run-stage-node {
  color: var(--blue);
}
.run-detail-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  border: 1px solid var(--tint-line);
  border-radius: var(--radius);
  background: var(--tint);
  margin-bottom: var(--space-20px);
}
.run-detail-stats article {
  padding: var(--space-16px) var(--space-18px);
  border-left: 1px solid var(--tint-line);
  min-width: 0;
}
.run-detail-stats article:first-child {
  border-left: 0;
}
.run-detail-stats article > span {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-6px);
  font-size: var(--font-13px);
  color: var(--t2);
}
.run-detail-stats strong {
  display: block;
  font-size: var(--font-28px);
  font-weight: 650;
  letter-spacing: -0.03em;
  line-height: 1.2;
  margin-top: var(--space-8px);
  overflow-wrap: anywhere;
}
.run-detail-stats strong small {
  margin-left: var(--space-4px);
  font-size: var(--font-13px);
  letter-spacing: 0;
}
.run-phase {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--space-12px);
  font-size: var(--font-14px);
  margin-bottom: var(--space-10px);
}
.run-phase > span {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-6px) var(--space-14px);
  align-items: baseline;
}
.run-phase strong {
  color: var(--blue);
  font-weight: 600;
}
.run-progress {
  height: 8px;
  overflow: hidden;
  border-radius: var(--radius-4px);
  background: var(--gray-bg);
  margin-bottom: var(--space-18px);
}
.run-progress > span {
  display: block;
  height: 100%;
  border-radius: var(--radius-4px);
  background: var(--blue);
}
.run-detail-times {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-14px);
  padding: var(--space-14px) 0;
}
.run-detail-times > div > small {
  display: block;
  margin-bottom: var(--space-4px);
}
.run-detail-note {
  font-size: var(--font-12px);
  color: var(--t3);
  line-height: 1.8;
  margin-bottom: var(--space-20px);
}
.run-detail-section {
  border-top: 1px solid var(--line);
  font-size: var(--font-13px);
}
.run-detail-section:last-child {
  border-bottom: 1px solid var(--line);
}
.run-detail-section > summary {
  padding: var(--space-16px) 0;
  cursor: pointer;
  color: var(--t1);
  font-size: var(--font-15px);
  font-weight: 500;
  overflow-wrap: anywhere;
}
.run-detail-section > summary > span {
  color: var(--t3);
  font-size: var(--font-12px);
  font-weight: 400;
  margin-left: var(--space-10px);
}
.run-detail-section > summary:focus-visible {
  outline-offset: -2px;
}
.run-section-content {
  padding: 0 0 var(--space-18px);
}
.run-detail .attempt-row:last-child {
  border-bottom: 0;
}
.run-detail .attempt-row:first-child {
  padding-top: var(--space-4px);
}
.run-detail .log-toolbar {
  margin-top: var(--space-4px);
  flex-wrap: wrap;
}
.run-detail .log-toolbar h3 {
  flex-basis: 100%;
  font-size: var(--font-13px);
  font-weight: 400;
  color: var(--t2);
}
.run-detail .log-actions {
  display: flex;
  gap: var(--space-6px);
  align-items: center;
  margin-left: auto;
}
.run-detail .snapshot pre {
  margin-top: var(--space-4px);
}
.run-detail-loading {
  padding: var(--space-20px) 0;
  font-size: var(--font-13px);
  color: var(--t3);
}
@media (max-width: 700px) {
  .run-detail-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .run-detail-stats article:nth-child(3) {
    border-left: 0;
  }
  .run-detail-stats article:nth-child(n + 3) {
    border-top: 1px solid var(--tint-line);
  }
  .run-detail-stats strong {
    font-size: var(--font-24px);
  }
  .run-detail .detail-title {
    flex-wrap: wrap;
  }
  .run-lifecycle li {
    flex-direction: column;
    gap: var(--space-4px);
  }
  .run-detail-times {
    gap: var(--space-8px);
    font-size: var(--font-12px);
  }
}
@media (pointer: coarse) {
  .run-detail button,
  .run-detail-section > summary {
    min-height: 44px;
  }
}
</style>
