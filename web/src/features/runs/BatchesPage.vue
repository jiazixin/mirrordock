<script setup lang="ts">
import { useQueryState } from '../../app/use-query-state'
import SelectField from '../../components/SelectField.vue'
import { computed, ref, onMounted, onBeforeUnmount } from 'vue'
import PaginationBar from '../../components/PaginationBar.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import ModalTransition from '../../components/ModalTransition.vue'
import { useRunPage } from './useRunPage'
import { timeRangeOptions } from './timeRanges'
import { Pause, Play, X, Info, Layers3, RotateCcw } from '@lucide/vue'
import StatusBadge from '../../components/StatusBadge.vue'
import RunDetail from './RunDetail.vue'
import { state, refresh, perform, runAction, rerunSource } from '../../app/state'
import { phases, sourceType, runStage } from '../../app/labels'
import { time } from '../../app/server-time'
import { formatSize } from '../../lib/format/bytes'
import { formatDuration, formatDurationMillis } from '../../lib/format/duration'
import type { Run, SourceType } from '../../app/types'
import { formatPercent } from '../../lib/format/number'
function received(progress: Run['progress']) {
  return typeof progress.received_bytes !== 'number'
    ? '未统计'
    : formatSize(progress.received_bytes)
}
function transferDescription(type: SourceType) {
  const descriptions: Record<SourceType, string> = {
    github_release:
      '本任务各次尝试实际读取的资产响应正文；不含本地复用、已有续传片段和 HTTP/TLS 头部。',
    rsync:
      '累计 Rsync 报告的 Total bytes received，包含协议数据；命令结束后更新。缺少统计的尝试不会推算。',
    reposync: 'DNF 尚未提供可靠的接收字节统计。',
  }
  return descriptions[type]
}
// One continuous bar: the executor's percent while it has one, otherwise empty
// before transfer and full once verification or publishing has started.
function barPercent(progress: Run['progress']) {
  if (typeof progress.percent === 'number') return Math.min(100, Math.max(0, progress.percent))
  return runStage(progress.phase) >= 2 ? 100 : 0
}
const selectedState = ref('')
const selectedSource = ref('')
const trigger = ref('')
const detail = ref<number>()
const age = ref('')
const { page, pageSize, total, records, loading, loadError, load } = useRunPage({
  state: selectedState,
  source: selectedSource,
  age,
  trigger,
})
useQueryState({
  state: selectedState,
  source: selectedSource,
  range: age,
  trigger,
  detail,
  page,
  pageSize,
})
const sourceOptions = computed(() => [
  { value: '', label: '全部同步源' },
  ...(state.config?.config.sources ?? []).map(({ id, name }) => ({ value: id, label: name })),
])
const items = computed(() =>
  records.value.map(
    (run) =>
      (state.connected ? state.liveRuns.find((live) => live.id === run.id) : undefined) ?? run,
  ),
)
let timer: ReturnType<typeof setInterval>
onMounted(() => {
  timer = setInterval(() => {
    if (!loading.value) void load()
  }, 5000)
})
onBeforeUnmount(() => {
  clearInterval(timer)
})
const groups = computed(() => {
  const out: Record<string, typeof items.value> = {}
  for (const run of items.value) {
    const day = time(run.created_at).slice(0, 10)
    ;(out[day] ??= []).push(run)
  }
  return Object.entries(out)
})
const stateOptions = [
  { value: '', label: '全部状态' },
  { value: 'queued', label: '排队中' },
  { value: 'running', label: '运行中' },
  { value: 'pausing', label: '正在暂停' },
  { value: 'paused', label: '已暂停' },
  { value: 'succeeded', label: '成功' },
  { value: 'failed', label: '失败' },
  { value: 'timed_out', label: '超时' },
  { value: 'interrupted', label: '中断' },
  { value: 'cancelled', label: '已取消' },
]
const triggerOptions = [
  { value: '', label: '全部来源' },
  { value: 'manual', label: '手动触发' },
  { value: 'schedule', label: '自动计划' },
]
function progressLabel(progress: Run['progress'], type: SourceType) {
  if (typeof progress.percent !== 'number') return phases[progress.phase] ?? '进行中'
  return `${type === 'rsync' ? '≈ ' : ''}${formatPercent(progress.percent, 0)}`
}
function canPause(status: string) {
  return ['running', 'queued'].includes(status)
}
function canCancel(status: string) {
  return ['queued', 'running', 'paused', 'interrupted', 'pausing'].includes(status)
}
function partialReceived(progress: Run['progress']) {
  return progress.received_incomplete && typeof progress.received_bytes === 'number'
}
</script>
<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>
          批次记录<span
            class="title-dot"
            aria-hidden="true"
            >.</span
          >
        </h1>
      </div>
      <RefreshButton
        class="button"
        :action="() => perform(refresh)"
        >刷新</RefreshButton
      >
    </div>
    <div class="panel batch-panel">
      <div class="filters-grid">
        <label
          >时间范围<SelectField
            v-model="age"
            aria-label="时间范围"
            :options="timeRangeOptions" /></label
        ><label
          >同步源<SelectField
            v-model="selectedSource"
            aria-label="同步源"
            :options="sourceOptions" /></label
        ><label
          >批次状态<SelectField
            v-model="selectedState"
            aria-label="批次状态"
            :options="stateOptions" /></label
        ><label
          >运行来源<SelectField
            v-model="trigger"
            aria-label="运行来源"
            :options="triggerOptions"
        /></label>
      </div>
      <div
        v-if="loadError"
        class="alert error"
        role="alert"
      >
        {{ loadError
        }}<button
          class="button compact"
          :disabled="loading"
          @click="load"
        >
          重试
        </button>
      </div>
      <p
        v-else-if="loading"
        role="status"
      >
        正在加载记录…
      </p>
      <div class="timeline">
        <div
          v-for="[day, runs] in groups"
          :key="day"
          class="day-group"
        >
          <div class="day-label"><span></span>{{ day }}</div>
          <article
            v-for="(run, index) in runs"
            :key="run.id"
            class="run-row"
            :class="{ live: run.state === 'running', tail: index === runs.length - 1 }"
          >
            <div class="run-time">{{ time(run.created_at).slice(-8, -3) }}</div>
            <div
              class="timeline-dot"
              :class="run.state"
            ></div>
            <div class="run-main">
              <div class="run-title">
                <strong>#{{ run.id }}</strong
                ><span
                  class="type-pill"
                  :class="run.type"
                  >{{ sourceType(run.type) }}</span
                ><StatusBadge :state="run.state" /><span class="run-duration"
                  >{{ ['running', 'pausing'].includes(run.state) ? '已运行 ' : ''
                  }}{{ formatDuration(run.elapsed_ms / 1000)
                  }}<span v-if="run.state === 'running'">
                    · 剩余约 {{ formatDurationMillis(run.progress.eta_ms) }}</span
                  ></span
                >
              </div>
              <p class="run-subtitle">
                {{ run.name }} · {{ run.trigger === 'schedule' ? '自动计划' : '手动触发' }} ·
                {{ phases[run.progress.phase] ?? '未知阶段'
                }}<span v-if="run.attempts.length > 1"> · 已尝试 {{ run.attempts.length }} 次</span>
              </p>
              <div
                v-if="run.state === 'running'"
                class="progress-line"
              >
                <div
                  class="progress-track"
                  role="progressbar"
                  aria-label="同步进度"
                  :aria-valuenow="barPercent(run.progress)"
                  :aria-valuemin="0"
                  :aria-valuemax="100"
                >
                  <span :style="{ width: barPercent(run.progress) + '%' }"></span>
                </div>
                <span>{{ progressLabel(run.progress, run.type) }}</span>
              </div>
              <div class="run-bottom">
                <div class="run-counters">
                  <span :title="transferDescription(run.type)"
                    >实际接收 {{ received(run.progress)
                    }}<template v-if="partialReceived(run.progress)">（部分统计）</template></span
                  ><span
                    v-if="run.type === 'github_release' || run.progress.success > 0"
                    class="counter success"
                    >完成 {{ run.progress.success }} 项</span
                  ><span
                    v-if="run.progress.skipped"
                    class="counter skipped"
                    >跳过 {{ run.progress.skipped }}</span
                  ><span
                    v-if="run.error_code"
                    class="counter failed"
                    >{{ run.error_code }}</span
                  ><span v-if="run.progress.speed_bytes_per_second"
                    >{{ formatSize(run.progress.speed_bytes_per_second) }}/s</span
                  >
                </div>
                <div class="row-actions">
                  <button
                    v-if="canPause(run.state)"
                    class="icon-button outlined tone-amber"
                    aria-label="暂停任务"
                    title="暂停任务"
                    @click="perform(() => runAction(run, 'pause'))"
                  >
                    <Pause :size="15" /></button
                  ><button
                    v-if="['paused', 'interrupted'].includes(run.state)"
                    class="icon-button outlined tone-green"
                    aria-label="继续任务"
                    title="继续任务"
                    @click="perform(() => runAction(run, 'resume'))"
                  >
                    <Play :size="15" /></button
                  ><button
                    v-if="['failed', 'timed_out', 'succeeded', 'cancelled'].includes(run.state)"
                    class="icon-button outlined tone-green"
                    aria-label="重新同步"
                    title="重新同步"
                    @click="perform(() => rerunSource(run.source_id, run.name))"
                  >
                    <RotateCcw :size="15" /></button
                  ><button
                    v-if="canCancel(run.state)"
                    class="icon-button outlined danger"
                    aria-label="取消任务"
                    title="取消任务"
                    @click="perform(() => runAction(run, 'cancel'))"
                  >
                    <X :size="15" /></button
                  ><button
                    class="icon-button outlined"
                    aria-label="查看运行详情"
                    title="查看运行详情"
                    @click="detail = run.id"
                  >
                    <Info :size="15" />
                  </button>
                </div>
              </div>
            </div>
          </article>
        </div>
        <div
          v-if="!items.length && !loading && !loadError"
          class="empty-state"
        >
          <Layers3 :size="32" />
          <h3>暂无运行批次</h3>
          <p>从同步源页面启动任务，进度会实时出现在这里。</p>
        </div>
      </div>
      <PaginationBar
        v-model:page="page"
        v-model:page-size="pageSize"
        :total="total"
        :count="items.length"
        :disabled="loading"
        noun="个批次"
      />
    </div>
    <ModalTransition>
      <RunDetail
        v-if="detail"
        :id="detail"
        @close="detail = undefined"
      />
    </ModalTransition>
  </section>
</template>

<style scoped>
.filters-grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 180px));
  gap: var(--space-14px);
}
.filters-grid :deep(label) {
  font-size: var(--font-13px);
  color: var(--t3);
}
.batch-panel {
  padding: var(--space-22px);
}
.timeline {
  margin-top: var(--space-24px);
}
.day-group + :deep(.day-group) {
  margin-top: var(--space-24px);
}
.day-label {
  font-size: var(--font-13px);
  font-weight: 600;
  color: var(--t2);
  padding: 0 0 var(--space-10px);
  border-bottom: 1px solid var(--line);
}
.day-label :deep(span) {
  display: none;
}
.run-row {
  display: flex;
  position: relative;
  padding: var(--space-16px) 0;
  gap: var(--space-16px);
  border-bottom: 1px solid var(--line);
}
.run-row.tail {
  border-bottom: 0;
}
.run-time {
  width: 44px;
  padding-top: var(--space-1px);
  color: var(--t3);
  font-size: var(--font-13px);
  flex-shrink: 0;
}
.timeline-dot {
  width: 6px;
  height: 6px;
  margin-top: var(--space-8px);
  border-radius: 50%;
  flex-shrink: 0;
  background: var(--t3);
}
.timeline-dot.running {
  background: var(--blue);
  animation: live-pulse var(--motion-2d4s) ease-in-out infinite;
}
.timeline-dot.failed,
.timeline-dot.timed_out {
  background: var(--red);
}
.timeline-dot.paused,
.timeline-dot.pausing,
.timeline-dot.interrupted {
  background: var(--amb);
}
.timeline-dot.queued {
  background: transparent;
  box-shadow: inset 0 0 0 1.5px var(--t3);
}
.run-main {
  width: 100%;
  min-width: 0;
}
.run-title {
  display: flex;
  gap: var(--space-10px);
  align-items: center;
  flex-wrap: wrap;
}
.run-title > :deep(strong) {
  font-size: var(--font-13px);
  color: var(--t3);
  font-weight: 400;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
}
.run-duration {
  margin-left: auto;
  font-size: var(--font-13px);
  color: var(--t2);
}
.run-duration > :deep(span) {
  color: var(--t3);
  margin-left: var(--space-6px);
}
.run-subtitle {
  font-size: var(--font-14px);
  color: var(--t1);
  line-height: 1.6;
  margin: var(--space-6px) 0;
}
.progress-line {
  display: flex;
  gap: var(--space-12px);
  align-items: center;
  margin: var(--space-10px) 0;
}
.progress-track {
  height: 6px;
  flex: 1;
  border-radius: var(--radius-3px);
  background: var(--gray-bg);
  overflow: hidden;
}
.progress-track > :deep(span) {
  display: block;
  height: 100%;
  border-radius: var(--radius-3px);
  background: var(--blue);
  transition: width var(--motion-0d6s);
}
.progress-line > :deep(span) {
  font-size: var(--font-12d5px);
  font-weight: 600;
  color: var(--blue);
  min-width: 32px;
  text-align: right;
}
.run-bottom {
  display: flex;
  gap: var(--space-10px);
  align-items: center;
  justify-content: space-between;
}
.run-bottom :deep(.row-actions) {
  margin-left: auto;
  flex-shrink: 0;
}
.run-counters {
  display: flex;
  align-items: center;
  gap: var(--space-4px) var(--space-16px);
  flex-wrap: wrap;
  color: var(--t3);
  font-size: var(--font-12px);
}
.run-counters > :deep(span),
.counter {
  display: inline-flex;
  align-items: center;
  gap: var(--space-6px);
}
.counter.success {
  color: var(--t2);
}
.counter.skipped {
  color: var(--t2);
}
.counter.failed {
  color: var(--red);
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
}
.batch-panel :deep(.pagination) {
  margin: var(--space-22px) var(--space--22px) var(--space--22px);
}
@media (max-width: 1180px) {
  .run-duration > :deep(span) {
    display: none;
  }
}
@media (max-width: 900px) {
  .filters-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 600px) {
  .batch-panel {
    padding: var(--space-14px);
  }
}
@media (max-width: 600px) {
  .batch-panel :deep(.pagination) {
    margin: var(--space-20px) var(--space--14px) var(--space--14px);
    padding: var(--space-14px);
  }
}
@media (max-width: 600px) {
  .run-time {
    display: none;
  }
}
@media (max-width: 600px) {
  .run-duration {
    width: 100%;
    margin-left: 0;
  }
}
@media (max-width: 600px) {
  .run-bottom {
    align-items: flex-end;
  }
}
@media (max-width: 360px) {
  .filters-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (forced-colors: active) {
  .timeline-dot {
    forced-color-adjust: none;
    background: CanvasText;
  }
}
</style>
