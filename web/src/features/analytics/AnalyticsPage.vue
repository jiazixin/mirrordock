<script setup lang="ts">
import { useQueryState } from '../../app/use-query-state'
import { errorText } from '../../app/errors'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  CheckCircle2,
  RefreshCw,
  ArrowDownToLine,
  ChartColumn,
  GitBranch,
  CircleAlert,
} from '@lucide/vue'
import SelectField from '../../components/SelectField.vue'
import RefreshButton from '../../components/RefreshButton.vue'
import TrendChart from './TrendChart.vue'
import { vTabIndicator } from '../../lib/motion/tabIndicator'
import { api } from '../../app/api'
import { state } from '../../app/state'
import { time } from '../../app/server-time'
import { formatSize } from '../../lib/format/bytes'
import { formatDuration } from '../../lib/format/duration'
import type { Analytics, AnalyticsStats, SourceType } from '../../app/types'
import { formatNumber, formatRate } from '../../lib/format/number'
const { kind } = defineProps<{ kind: SourceType }>()
const days = ref('30'),
  source = ref(''),
  metric = ref<'runs' | 'bytes' | 'duration'>('runs')
useQueryState(
  { days, source, metric },
  { days: ['7', '30', '90'], metric: ['runs', 'bytes', 'duration'] },
)
const data = ref<Analytics | null>(null),
  loading = ref(false),
  error = ref('')
let controller: AbortController | undefined,
  requestID = 0
const github = computed(() => kind === 'github_release')
const reposync = computed(() => kind === 'reposync')
const kindInfo = computed(
  () =>
    ({
      rsync: { insight: '来源可靠性', icon: GitBranch },
      reposync: { insight: '执行与重试', icon: RefreshCw },
      github_release: { insight: '资产完成与复用', icon: ArrowDownToLine },
    })[kind],
)
const trendMetrics = computed(() =>
  reposync.value
    ? ([
        { key: 'runs', label: '次数' },
        { key: 'duration', label: '耗时' },
      ] as const)
    : ([
        { key: 'runs', label: '次数' },
        { key: 'bytes', label: '处理量' },
        { key: 'duration', label: '耗时' },
      ] as const),
)
const sourceOptions = computed(() => {
  const names = new Map<string, string>()
  for (const item of data.value?.sources ?? []) names.set(item.id, item.name)
  for (const item of state.config?.config.sources ?? [])
    if (item.type === kind) names.set(item.id, item.name)
  if (source.value && !names.has(source.value)) names.set(source.value, source.value)
  return [
    { value: '', label: '全部同步源' },
    ...Array.from(names, ([value, label]) => ({ value, label })),
  ]
})
const evaluated = (s: AnalyticsStats) => s.succeeded + s.failed
const rate = (s: AnalyticsStats) =>
  evaluated(s) ? `${formatRate(s.succeeded, evaluated(s))}%` : '—'
const average = (s: AnalyticsStats) =>
  s.duration_samples ? formatDuration(s.elapsed_ms / 1000 / s.duration_samples) : '—'
const resultParts = computed(() =>
  data.value
    ? [
        { label: '成功', count: data.value.summary.succeeded, className: 'succeeded' },
        { label: '失败 / 超时', count: data.value.summary.failed, className: 'failed' },
        { label: '已取消', count: data.value.summary.cancelled, className: 'cancelled' },
        { label: '未结束', count: data.value.summary.active, className: 'active' },
      ]
    : [],
)
const ring = computed(() => {
  const summary = data.value?.summary
  const total = summary?.runs || 1
  const succeeded = (100 * (summary?.succeeded ?? 0)) / total
  const failed = succeeded + (100 * (summary?.failed ?? 0)) / total
  const cancelled = failed + (100 * (summary?.cancelled ?? 0)) / total
  return {
    '--succeeded-end': `${succeeded}%`,
    '--failed-end': `${failed}%`,
    '--cancelled-end': `${cancelled}%`,
  }
})
// Share of verified assets that were newly transferred versus fully reused.
const assetShares = computed(() => {
  const summary = data.value?.summary
  if (!summary?.completed) return { transferred: 0, reused: 0 }
  return {
    transferred: (100 * (summary.completed - summary.reused)) / summary.completed,
    reused: (100 * summary.reused) / summary.completed,
  }
})
const largestError = computed(() => Math.max(1, ...(data.value?.errors.map((e) => e.count) ?? [])))
async function load() {
  controller?.abort()
  const current = ++requestID
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await api<Analytics>({
      path: `/analytics?type=${kind}&days=${days.value}&source=${encodeURIComponent(source.value)}`,
      signal: controller.signal,
    })
    if (current === requestID) data.value = result
  } catch (e) {
    if (current === requestID && !controller.signal.aborted) error.value = errorText(e)
  } finally {
    if (current === requestID) loading.value = false
  }
}
watch(
  () => kind,
  () => {
    source.value = ''
    metric.value = 'runs'
  },
)
watch(
  [() => kind, days, source],
  () => {
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  ++requestID
  controller?.abort()
})
const dayOptions = [
  { value: '7', label: '近 7 天' },
  { value: '30', label: '近 30 天' },
  { value: '90', label: '近 90 天' },
]
const metricNote = computed(() =>
  metric.value === 'bytes'
    ? '仅汇总成功任务的执行器字节快照，不代表实际网络流量。'
    : '成功、失败和超时任务的累计执行时间均值；无样本日期以 0 绘制。',
)
const assetNote = computed(() => {
  if (!data.value) return ''
  const { completed, reused } = data.value.summary
  return `新增或续传 ${completed - reused} 项，完整复用 ${reused} 项`
})
</script>
<template>
  <section class="analytics-page">
    <header class="page-heading">
      <h1>
        数据分析<span
          class="title-dot"
          aria-hidden="true"
          >.</span
        >
      </h1>
      <RefreshButton
        class="button"
        :disabled="loading"
        :spinning="loading"
        :action="load"
        :size="15"
        >刷新</RefreshButton
      >
    </header>
    <nav
      v-tab-indicator
      class="tabs analytics-tabs"
      aria-label="分析类型"
    >
      <RouterLink
        :to="{ path: '/analytics/rsync', query: { days: days === '30' ? undefined : days } }"
        >Rsync 分析</RouterLink
      ><RouterLink
        :to="{ path: '/analytics/reposync', query: { days: days === '30' ? undefined : days } }"
        >Reposync 分析</RouterLink
      ><RouterLink
        :to="{ path: '/analytics/github', query: { days: days === '30' ? undefined : days } }"
        >Release 分析</RouterLink
      >
    </nav>
    <div class="analytics-filters">
      <SelectField
        v-model="days"
        aria-label="分析时间范围"
        :options="dayOptions"
      />
      <SelectField
        v-model="source"
        aria-label="分析同步源"
        :options="sourceOptions"
      />
      <span
        v-if="data"
        class="analytics-updated"
        >更新于 {{ time(data.until) }}</span
      >
    </div>
    <div
      v-if="error"
      class="analytics-message error"
      role="alert"
    >
      <CircleAlert :size="18" /><span>{{ error }}</span
      ><button
        class="text-button"
        @click="load"
      >
        重试
      </button>
    </div>
    <Transition
      name="page"
      mode="out-in"
    >
      <div
        v-if="!data"
        key="loading"
        class="analytics-message"
        role="status"
      >
        {{ loading ? '正在汇总运行记录…' : '尚未取得分析数据' }}
      </div>
      <div
        v-else
        key="data"
      >
        <div class="stat-grid analytics-stats">
          <article>
            <span>运行次数</span><strong>{{ formatNumber(data.summary.runs) }}</strong
            ><small>{{ data.summary.active }} 次未结束 · {{ data.summary.cancelled }} 次取消</small>
          </article>
          <article>
            <span>运行成功率</span><strong>{{ rate(data.summary) }}</strong
            ><small
              >{{ data.summary.succeeded }} 成功 / {{ evaluated(data.summary) }} 次有效结果</small
            >
          </article>
          <article>
            <span>平均执行耗时</span><strong>{{ average(data.summary) }}</strong
            ><small>{{ data.summary.duration_samples }} 个样本 · 不含排队与暂停</small>
          </article>
          <article v-if="reposync">
            <span>自动重试任务</span><strong>{{ formatNumber(data.summary.retried_runs) }}</strong
            ><small>期间发生过自动重试的任务数</small>
          </article>
          <article v-else>
            <span>成功任务处理量</span
            ><strong class="bytes-stat">{{ formatSize(data.summary.transferred_bytes) }}</strong
            ><small>{{ github ? '包含复用资产的有效字节' : 'Rsync 报告的处理字节' }}</small>
          </article>
        </div>
        <div
          v-if="!data.summary.runs"
          class="analytics-empty"
        >
          <ChartColumn :size="28" />
          <div>
            <h3>这段时间暂无运行记录</h3>
            <p>同步任务产生记录后，趋势和结果会显示在这里。可调整时间范围或同步源。</p>
          </div>
          <RouterLink
            class="button"
            to="/sources"
            >查看同步源</RouterLink
          >
        </div>
        <div class="analytics-grid">
          <article class="panel trend-panel">
            <div class="analytics-panel-heading">
              <div>
                <h3>运行趋势</h3>
                <small>{{ data.days[0]?.date }} — {{ data.days.at(-1)?.date }}</small>
              </div>
              <div
                class="chart-tabs"
                role="group"
                aria-label="趋势指标"
              >
                <button
                  v-for="item in trendMetrics"
                  :key="item.key"
                  :aria-pressed="metric === item.key"
                  @click="metric = item.key"
                >
                  {{ item.label }}
                </button>
              </div>
            </div>
            <TrendChart
              :days="data.days"
              :metric="metric"
            />
            <div
              v-if="metric === 'runs'"
              class="chart-legend"
            >
              <span
                v-for="part in resultParts"
                :key="part.label"
                ><i :class="part.className" />{{ part.label }}</span
              >
            </div>
            <p
              v-else
              class="chart-footnote"
            >
              {{ metricNote }}
            </p>
          </article>
          <article class="panel result-panel">
            <div class="analytics-panel-heading">
              <h3>运行结果</h3>
              <small>全部 {{ data.summary.runs }} 次</small>
            </div>
            <div class="result-content">
              <div
                class="result-ring"
                :class="{ empty: !data.summary.runs }"
                :style="ring"
                role="img"
                :aria-label="resultParts.map((p) => `${p.label} ${p.count} 次`).join('，')"
              >
                <div>
                  <strong>{{ rate(data.summary) }}</strong
                  ><small>成功率</small>
                </div>
              </div>
              <div class="result-legend">
                <div
                  v-for="part in resultParts"
                  :key="part.label"
                >
                  <i :class="part.className" /><span>{{ part.label }}</span
                  ><strong>{{ part.count }}</strong>
                </div>
              </div>
            </div>
            <p class="chart-footnote">
              成功率 = 成功 ÷（成功 + 失败 + 超时）。取消、中断及未结束任务不计入。
            </p>
          </article>
          <article class="panel insight-panel">
            <div class="analytics-panel-heading">
              <h3>{{ kindInfo.insight }}</h3>
              <component
                :is="kindInfo.icon"
                :size="18"
              />
            </div>
            <template v-if="github"
              ><div class="insight-total">
                <strong>{{ formatNumber(data.summary.completed) }}</strong
                ><span>成功任务中已验证资产</span>
              </div>
              <div
                class="asset-track"
                role="img"
                :aria-label="assetNote"
              >
                <span :style="{ width: `${assetShares.transferred}%` }" /><span
                  :style="{ width: `${assetShares.reused}%` }"
                />
              </div>
              <div class="asset-labels">
                <span
                  ><i />新增 / 续传
                  <strong>{{ data.summary.completed - data.summary.reused }}</strong></span
                ><span
                  ><i />完整复用 <strong>{{ data.summary.reused }}</strong></span
                >
              </div>
              <div class="insight-divider asset-retries">
                <span>发生自动重试的任务</span><strong>{{ data.summary.retried_runs }} 次</strong>
              </div>
              <p class="chart-footnote">
                按运行累计，同一资产可在不同任务中重复计入；复用为已验证资产的子集，不推算节省流量。
              </p></template
            >
            <template v-else-if="reposync"
              ><div class="reliability-stats">
                <div>
                  <strong>{{ data.summary.attempts }}</strong
                  ><span>执行尝试</span>
                </div>
                <div>
                  <strong>{{ data.summary.failed_attempts }}</strong
                  ><span>失败尝试</span>
                </div>
                <div>
                  <strong>{{ data.summary.retried_runs }}</strong
                  ><span>自动重试任务</span>
                </div>
              </div>
              <p class="chart-footnote">
                尝试包含暂停后继续；失败尝试排除主动暂停、取消及服务中断。重试任务按任务计数，多次重试只计一次。
              </p></template
            >
            <template v-else
              ><div class="reliability-stats">
                <div>
                  <strong>{{ data.summary.attempts }}</strong
                  ><span>来源尝试</span>
                </div>
                <div>
                  <strong>{{ data.summary.failed_attempts }}</strong
                  ><span>失败尝试</span>
                </div>
                <div>
                  <strong>{{ data.summary.fallback_succeeded }}</strong
                  ><span>备用源成功任务</span>
                </div>
              </div>
              <div class="insight-divider">
                <span>发生自动重试的任务</span><strong>{{ data.summary.retried_runs }} 次</strong>
              </div>
              <p class="chart-footnote">
                尝试包含暂停后继续；失败尝试排除主动暂停、取消及服务中断。备用源成功指最终通过非首个来源完成的任务。
              </p></template
            >
          </article>
          <article class="panel errors-panel">
            <div class="analytics-panel-heading">
              <h3>失败原因</h3>
              <small>按任务错误码 · 前 8 项</small>
            </div>
            <div
              v-if="!data.errors.length"
              class="chart-empty"
            >
              <CheckCircle2 :size="23" /><span>当前范围没有失败记录</span>
            </div>
            <div
              v-else
              class="error-bars"
            >
              <div
                v-for="item in data.errors"
                :key="item.code"
              >
                <div>
                  <code>{{ item.code }}</code
                  ><strong>{{ item.count }} 次</strong>
                </div>
                <div class="error-track">
                  <span :style="{ width: `${(100 * item.count) / largestError}%` }" />
                </div>
              </div>
            </div>
          </article>
        </div>
        <article class="panel source-ranking">
          <div class="analytics-panel-heading">
            <h3>同步源运行排行</h3>
            <small>按运行次数 · 前 20 项</small>
          </div>
          <div class="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>同步源</th>
                  <th>运行次数</th>
                  <th>成功率</th>
                  <th>平均耗时</th>
                  <th v-if="reposync">自动重试任务</th>
                  <th v-if="!reposync">成功任务处理量</th>
                  <th v-if="!reposync">{{ github ? '已验证 / 复用资产' : '传输文件数' }}</th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="item in data.sources"
                  :key="item.id"
                >
                  <td>
                    <strong>{{ item.name }}</strong
                    ><small>{{ item.id }}</small>
                  </td>
                  <td>{{ item.runs }}</td>
                  <td>{{ rate(item) }}</td>
                  <td>{{ average(item) }}</td>
                  <td v-if="reposync">{{ item.retried_runs }}</td>
                  <td v-if="!reposync">{{ formatSize(item.transferred_bytes) }}</td>
                  <td v-if="!reposync">
                    {{ formatNumber(item.completed)
                    }}<template v-if="github"> / {{ formatNumber(item.reused) }}</template>
                  </td>
                </tr>
                <tr v-if="!data.sources.length">
                  <td
                    :colspan="reposync ? 5 : 6"
                    class="ranking-empty"
                  >
                    暂无同步源运行数据
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </article>
        <p class="analytics-note">
          按任务创建日期归入程序时区
          {{
            data.timezone
          }}，范围含今天截至更新时间。历史源以运行记录保留；清理原始日志不会影响分析。<template
            v-if="reposync"
            >包数、复用量和传输字节尚无可靠统计，暂不展示。</template
          ><template v-else
            >文件与字节仅累计成功任务的最终快照，未知统计不推算，{{
              github ? '资产数量' : '传输文件数'
            }}为执行器报告值。</template
          >
        </p>
      </div>
    </Transition>
  </section>
</template>
<style scoped>
.succeeded {
  background: var(--chart-ok);
}
.failed {
  background: var(--red);
}
.cancelled {
  background: var(--line-strong);
}
.active {
  background: var(--amb);
}
.result-ring:not(.empty) {
  background: conic-gradient(
    var(--chart-ok) 0 var(--succeeded-end),
    var(--red) var(--succeeded-end) var(--failed-end),
    var(--line-strong) var(--failed-end) var(--cancelled-end),
    var(--amb) var(--cancelled-end) 100%
  );
}

.analytics-tabs {
  margin-top: var(--space--8px);
}
.analytics-filters {
  display: flex;
  align-items: center;
  gap: var(--space-10px);
  margin-bottom: var(--space-32px);
}
.analytics-filters > .select-field:first-child {
  width: 150px;
}
.analytics-filters > .select-field:nth-child(2) {
  width: 220px;
}
.analytics-updated {
  margin-left: auto;
  font-size: var(--font-12px);
  color: var(--t3);
}
.analytics-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(0, 1fr);
  gap: var(--space-20px);
  margin-bottom: var(--space-20px);
}
.analytics-grid > .panel {
  min-width: 0;
  padding: var(--space-22px) var(--space-24px);
}
.analytics-panel-heading {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: var(--space-12px);
  margin-bottom: var(--space-18px);
}
.analytics-panel-heading > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-4px) var(--space-12px);
}
.analytics-panel-heading h3 {
  font-size: var(--font-15px);
}
.analytics-panel-heading > svg {
  align-self: center;
  color: var(--t3);
}
.chart-tabs {
  display: flex;
  border: 1px solid var(--line-strong);
  border-radius: var(--radius-4px);
  padding: var(--space-2px);
  gap: var(--space-2px);
}
.chart-tabs button {
  background: none;
  border: 0;
  padding: var(--space-3px) var(--space-10px);
  color: var(--t2);
  font-size: var(--font-12px);
  border-radius: var(--radius-3px);
  white-space: nowrap;
}
.chart-tabs button[aria-pressed='true'] {
  background: var(--hover);
  color: var(--t1);
}
.chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-16px);
  justify-content: flex-start;
  margin-top: var(--space-6px);
  font-size: var(--font-12px);
  color: var(--t3);
}
.chart-legend span {
  display: flex;
  gap: var(--space-7px);
  align-items: center;
}
i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: var(--radius-1px);
  flex-shrink: 0;
}
.chart-footnote {
  font-size: var(--font-12px);
  color: var(--t3);
  line-height: 1.8;
  margin-top: var(--space-18px);
}
.result-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-28px);
  min-height: 210px;
}
.result-ring {
  width: 148px;
  height: 148px;
  flex-shrink: 0;
  border-radius: 50%;
  padding: var(--space-10px);
}
.result-ring.empty {
  background: var(--line);
}
.result-ring > div {
  height: 100%;
  width: 100%;
  border-radius: 50%;
  background: var(--panel);
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.result-ring strong {
  font-size: var(--font-26px);
  font-weight: 650;
  letter-spacing: -0.03em;
}
.result-ring small {
  margin-top: var(--space-2px);
}
.result-legend {
  display: grid;
  gap: var(--space-14px);
  min-width: 0;
  font-size: var(--font-12px);
}
.result-legend > div {
  display: flex;
  align-items: center;
  gap: var(--space-8px);
  color: var(--t2);
}
.result-legend strong {
  margin-left: auto;
  padding-left: var(--space-16px);
  color: var(--t1);
  font-size: var(--font-13px);
  font-weight: 400;
}
.insight-total {
  display: flex;
  align-items: baseline;
  gap: var(--space-12px);
  margin: var(--space-24px) 0;
}
.insight-total strong {
  font-size: var(--font-36px);
  font-weight: 650;
  letter-spacing: -0.03em;
}
.insight-total span {
  font-size: var(--font-12px);
  color: var(--t3);
}
.asset-track {
  display: flex;
  height: 8px;
  overflow: hidden;
  border-radius: var(--radius-4px);
  background: var(--gray-bg);
}
.asset-track > span:first-child {
  background: var(--chart-ok);
}
.asset-track > span:last-child {
  background: var(--bar);
}
.asset-labels {
  display: flex;
  justify-content: space-between;
  gap: var(--space-15px);
  flex-wrap: wrap;
  font-size: var(--font-12px);
  color: var(--t2);
  margin-top: var(--space-12px);
}
.asset-labels span {
  display: flex;
  align-items: center;
  gap: var(--space-7px);
}
.asset-labels strong {
  font-weight: 400;
  color: var(--t1);
  margin-left: var(--space-6px);
}
.asset-labels i {
  background: var(--chart-ok);
}
.asset-labels span:last-child i {
  background: var(--bar);
}
.reliability-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: var(--space-16px);
  margin: var(--space-22px) 0;
}
.reliability-stats > div {
  display: flex;
  flex-direction: column;
  gap: var(--space-6px);
}
.reliability-stats strong {
  font-size: var(--font-32px);
  font-weight: 650;
  letter-spacing: -0.03em;
}
.reliability-stats span {
  font-size: var(--font-12px);
  color: var(--t3);
}
.insight-divider {
  display: flex;
  justify-content: space-between;
  border-top: 1px solid var(--line);
  padding-top: var(--space-14px);
  font-size: var(--font-13px);
  color: var(--t2);
}
.asset-retries {
  margin-top: var(--space-20px);
}
.insight-divider strong {
  font-weight: 400;
  color: var(--t1);
}
.error-bars {
  display: grid;
  gap: var(--space-16px);
}
.error-bars > div > div:first-child {
  display: flex;
  justify-content: space-between;
  gap: var(--space-12px);
  margin-bottom: var(--space-7px);
  color: var(--t2);
  font-size: var(--font-12px);
}
.error-bars code {
  font-size: var(--font-12px);
  overflow-wrap: anywhere;
  min-width: 0;
  color: var(--t1);
}
.error-bars strong {
  font-weight: 400;
  white-space: nowrap;
}
.error-track {
  height: 2px;
  background: var(--line);
  overflow: hidden;
}
.error-track span {
  display: block;
  height: 100%;
  background: var(--red);
}
.chart-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 135px;
  gap: var(--space-12px);
  font-size: var(--font-13px);
  color: var(--t3);
}
.chart-empty svg {
  color: var(--t3);
}
.source-ranking .analytics-panel-heading {
  padding: var(--space-22px) var(--space-24px) 0;
}
.source-ranking table {
  min-width: 740px;
  table-layout: fixed;
}
.source-ranking th,
.source-ranking td {
  text-align: center;
}
.source-ranking th:first-child {
  width: 220px;
}
.source-ranking th:first-child,
.source-ranking td:first-child {
  padding-left: var(--space-24px);
}
.source-ranking th:last-child,
.source-ranking td:last-child {
  padding-right: var(--space-24px);
}
.source-ranking td:first-child strong,
.source-ranking td:first-child small {
  overflow-wrap: anywhere;
}
.ranking-empty {
  padding: var(--space-32px);
  color: var(--t3);
}
.analytics-note {
  font-size: var(--font-12px);
  line-height: 1.9;
  color: var(--t3);
  margin-top: var(--space-16px);
}
.analytics-message {
  display: flex;
  align-items: center;
  gap: var(--space-10px);
  padding: var(--space-32px) 0;
  border-top: 1px solid var(--line-strong);
  color: var(--t3);
}
.analytics-message.error {
  color: var(--red);
}
.analytics-empty {
  display: flex;
  align-items: center;
  gap: var(--space-16px);
  padding: var(--space-20px) 0;
  border-bottom: 1px solid var(--line);
  margin-bottom: var(--space-24px);
  color: var(--t3);
}
.analytics-empty h3 {
  font-size: var(--font-14px);
  font-weight: 400;
}
.analytics-empty p {
  font-size: var(--font-13px);
  color: var(--t3);
  margin-top: var(--space-4px);
}
.analytics-empty .button {
  margin-left: auto;
}
@media (max-width: 1250px) {
  .result-content {
    flex-direction: column;
    gap: var(--space-18px);
    min-height: 240px;
  }
  .result-legend {
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: var(--space-12px);
  }
}
@media (max-width: 900px) {
  .analytics-grid {
    grid-template-columns: minmax(0, 1fr);
  }
  .result-content {
    flex-direction: row;
    min-height: 180px;
  }
  .result-legend {
    width: auto;
    grid-template-columns: 1fr;
    gap: var(--space-14px);
  }
  .analytics-updated {
    display: none;
  }
}
@media (max-width: 600px) {
  .analytics-tabs {
    gap: var(--space-20px);
    overflow-x: auto;
  }
  .analytics-filters {
    flex-wrap: wrap;
  }
  .analytics-filters > .select-field:first-child,
  .analytics-filters > .select-field:nth-child(2) {
    flex: 1;
    min-width: 120px;
    width: auto;
  }
  .analytics-grid > .panel {
    padding: var(--space-18px) var(--space-16px);
  }
  .source-ranking .analytics-panel-heading {
    padding: var(--space-18px) var(--space-16px) 0;
  }
  .source-ranking th:first-child,
  .source-ranking td:first-child {
    padding-left: var(--space-16px);
  }
  .source-ranking th:last-child,
  .source-ranking td:last-child {
    padding-right: var(--space-16px);
  }
  .trend-panel .analytics-panel-heading {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .result-content {
    gap: var(--space-16px);
    flex-direction: column;
  }
  .result-legend {
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .analytics-empty {
    flex-wrap: wrap;
  }
  .analytics-empty .button {
    margin-left: 0;
  }
  .analytics-page .page-heading .button {
    flex-shrink: 0;
  }
}
.text-button {
  display: inline-flex;
  border: 0;
  background: transparent;
  color: var(--brand);
  font-weight: 600;
  padding: var(--space-6px) 0;
  font-size: var(--font-13d5px);
  line-height: 1.4;
}
.text-button:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}
</style>
