<script setup lang="ts">
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
import TrendChart from './TrendChart.vue'
import { api } from '../../app/api'
import { state } from '../../app/state'
import { bytes, duration, time } from '../../app/format'
import type { Analytics, AnalyticsStats, SourceType } from '../../app/types'
const props = defineProps<{ kind: SourceType }>()
const days = ref('30'),
  source = ref(''),
  metric = ref<'runs' | 'bytes' | 'duration'>('runs')
const data = ref<Analytics | null>(null),
  loading = ref(false),
  error = ref('')
let controller: AbortController | undefined,
  requestID = 0
const github = computed(() => props.kind === 'github_release')
const reposync = computed(() => props.kind === 'reposync')
const kindInfo = computed(
  () =>
    ({
      rsync: { title: 'Rsync 分析', insight: '来源可靠性', icon: GitBranch },
      reposync: { title: 'Reposync分析', insight: '执行与重试', icon: RefreshCw },
      github_release: { title: 'Release分析', insight: '资产完成与复用', icon: ArrowDownToLine },
    })[props.kind],
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
    if (item.type === props.kind) names.set(item.id, item.name)
  if (source.value && !names.has(source.value)) names.set(source.value, source.value)
  return [
    { value: '', label: '全部同步源' },
    ...Array.from(names, ([value, label]) => ({ value, label })),
  ]
})
const evaluated = (s: AnalyticsStats) => s.succeeded + s.failed
const rate = (s: AnalyticsStats) =>
  evaluated(s) ? `${((100 * s.succeeded) / evaluated(s)).toFixed(1)}%` : '—'
const average = (s: AnalyticsStats) =>
  s.duration_samples ? duration(s.elapsed_seconds / s.duration_samples) : '—'
const resultParts = computed(() =>
  data.value
    ? [
        { label: '成功', count: data.value.summary.succeeded, color: 'var(--t2)' },
        { label: '失败 / 超时', count: data.value.summary.failed, color: 'var(--red)' },
        { label: '已取消', count: data.value.summary.cancelled, color: 'var(--line-strong)' },
        { label: '未结束', count: data.value.summary.active, color: 'var(--amb)' },
      ]
    : [],
)
const ring = computed(() => {
  let offset = 0
  return `conic-gradient(${resultParts.value
    .map((part) => {
      const begin = offset
      offset += (100 * part.count) / (data.value?.summary.runs || 1)
      return `${part.color} ${begin}% ${offset}%`
    })
    .join(',')})`
})
const largestSource = computed(() => Math.max(1, ...(data.value?.sources.map((s) => s.runs) ?? [])))
const largestError = computed(() => Math.max(1, ...(data.value?.errors.map((e) => e.count) ?? [])))
async function load() {
  controller?.abort()
  const current = ++requestID
  controller = new AbortController()
  loading.value = true
  error.value = ''
  try {
    const result = await api<Analytics>(
      `/analytics?type=${props.kind}&days=${days.value}&source=${encodeURIComponent(source.value)}`,
      'GET',
      undefined,
      controller.signal,
    )
    if (current === requestID) data.value = result
  } catch (e) {
    if (current === requestID && !controller.signal.aborted)
      error.value = e instanceof Error ? e.message : String(e)
  } finally {
    if (current === requestID) loading.value = false
  }
}
watch(
  () => props.kind,
  () => {
    source.value = ''
    data.value = null
    metric.value = 'runs'
  },
)
watch(
  [() => props.kind, days, source],
  () => {
    data.value = null
    void load()
  },
  { immediate: true },
)
onBeforeUnmount(() => {
  ++requestID
  controller?.abort()
})
</script>
<template>
  <section class="analytics-page">
    <header class="page-heading">
      <h1>{{ kindInfo.title }}</h1>
      <button
        class="button"
        :disabled="loading"
        @click="load"
      >
        <RefreshCw
          :size="15"
          :class="{ spinning: loading }"
        />刷新
      </button>
    </header>
    <nav
      class="tabs analytics-tabs"
      aria-label="分析类型"
    >
      <RouterLink to="/analytics/rsync">Rsync 分析</RouterLink
      ><RouterLink to="/analytics/reposync">Reposync分析</RouterLink
      ><RouterLink to="/analytics/github">Release分析</RouterLink>
    </nav>
    <div class="analytics-filters">
      <SelectField
        v-model="days"
        aria-label="分析时间范围"
        :options="[
          { value: '7', label: '近 7 天' },
          { value: '30', label: '近 30 天' },
          { value: '90', label: '近 90 天' },
        ]"
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
    <div
      v-else-if="!data"
      class="analytics-message"
      role="status"
    >
      正在汇总运行记录…
    </div>
    <template v-if="data && !error">
      <div class="stat-grid analytics-stats">
        <article>
          <span>运行次数</span><strong>{{ data.summary.runs.toLocaleString() }}</strong
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
          <span>自动重试任务</span><strong>{{ data.summary.retried_runs.toLocaleString() }}</strong
          ><small>期间发生过自动重试的任务数</small>
        </article>
        <article v-else>
          <span>成功任务处理量</span
          ><strong class="bytes-stat">{{ bytes(data.summary.bytes) }}</strong
          ><small>{{ github ? '包含复用资产的有效字节' : 'rsync 报告的处理字节' }}</small>
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
              ><i :style="{ background: part.color }" />{{ part.label }}</span
            >
          </div>
          <p
            v-else
            class="chart-footnote"
          >
            {{
              metric === 'bytes'
                ? '仅汇总成功任务的执行器字节快照，不代表实际网络流量。'
                : '成功、失败和超时任务的累计执行时间均值；无样本日期以 0 绘制。'
            }}
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
              :style="data.summary.runs ? { background: ring } : {}"
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
                <i :style="{ background: part.color }" /><span>{{ part.label }}</span
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
              <strong>{{ data.summary.completed.toLocaleString() }}</strong
              ><span>成功任务中已验证资产</span>
            </div>
            <div
              class="asset-track"
              role="img"
              :aria-label="`新增或续传 ${data.summary.completed - data.summary.reused} 项，完整复用 ${data.summary.reused} 项`"
            >
              <span
                :style="{
                  width: `${data.summary.completed ? (100 * (data.summary.completed - data.summary.reused)) / data.summary.completed : 0}%`,
                }"
              /><span
                :style="{
                  width: `${data.summary.completed ? (100 * data.summary.reused) / data.summary.completed : 0}%`,
                }"
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
                <th class="text-left">同步源</th>
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
                <td class="text-left">
                  <strong>{{ item.name }}</strong
                  ><small>{{ item.id }}</small>
                </td>
                <td>
                  <div class="ranking-count">
                    <strong>{{ item.runs }}</strong>
                    <div><span :style="{ width: `${(100 * item.runs) / largestSource}%` }" /></div>
                  </div>
                </td>
                <td>{{ rate(item) }}</td>
                <td>{{ average(item) }}</td>
                <td v-if="reposync">{{ item.retried_runs }}</td>
                <td v-if="!reposync">{{ bytes(item.bytes) }}</td>
                <td v-if="!reposync">
                  {{ item.completed.toLocaleString()
                  }}<template v-if="github"> / {{ item.reused.toLocaleString() }}</template>
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
    </template>
  </section>
</template>
<style scoped>
.analytics-tabs {
  margin-top: -8px;
}
.analytics-filters {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 32px;
}
.analytics-filters > .select-field:first-child {
  width: 150px;
}
.analytics-filters > .select-field:nth-child(2) {
  width: 220px;
}
.analytics-updated {
  margin-left: auto;
  font-size: 12px;
  color: var(--t3);
}
.analytics-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(0, 1fr);
  gap: 20px;
  margin-bottom: 20px;
}
.analytics-grid > .panel {
  min-width: 0;
  padding: 22px 24px;
}
.analytics-panel-heading {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 18px;
}
.analytics-panel-heading > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
}
.analytics-panel-heading h3 {
  font-size: 15px;
}
.analytics-panel-heading > svg {
  color: var(--t3);
}
.chart-tabs {
  display: flex;
  border: 1px solid var(--line-strong);
  border-radius: 4px;
  padding: 2px;
  gap: 2px;
}
.chart-tabs button {
  background: none;
  border: 0;
  padding: 3px 10px;
  color: var(--t2);
  font-size: 12px;
  border-radius: 3px;
  white-space: nowrap;
}
.chart-tabs button[aria-pressed='true'] {
  background: var(--hover);
  color: var(--t1);
}
.chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  justify-content: flex-start;
  margin-top: 6px;
  font-size: 12px;
  color: var(--t3);
}
.chart-legend span {
  display: flex;
  gap: 7px;
  align-items: center;
}
i {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 1px;
  flex-shrink: 0;
}
.chart-footnote {
  font-size: 12px;
  color: var(--t3);
  line-height: 1.8;
  margin-top: 18px;
}
.result-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 28px;
  min-height: 210px;
}
.result-ring {
  width: 148px;
  height: 148px;
  flex-shrink: 0;
  border-radius: 50%;
  padding: 10px;
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
  font-size: 26px;
  font-weight: 300;
  letter-spacing: -0.03em;
}
.result-ring small {
  margin-top: 2px;
}
.result-legend {
  display: grid;
  gap: 14px;
  min-width: 0;
  font-size: 12px;
}
.result-legend > div {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--t2);
}
.result-legend strong {
  margin-left: auto;
  padding-left: 16px;
  color: var(--t1);
  font-size: 13px;
  font-weight: 400;
}
.insight-total {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin: 24px 0;
}
.insight-total strong {
  font-size: 36px;
  font-weight: 300;
  letter-spacing: -0.03em;
}
.insight-total span {
  font-size: 12px;
  color: var(--t3);
}
.asset-track {
  display: flex;
  height: 8px;
  overflow: hidden;
  background: var(--line);
}
.asset-track > span:first-child {
  background: var(--t2);
}
.asset-track > span:last-child {
  background: var(--bar);
}
.asset-labels {
  display: flex;
  justify-content: space-between;
  gap: 15px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--t2);
  margin-top: 12px;
}
.asset-labels span {
  display: flex;
  align-items: center;
  gap: 7px;
}
.asset-labels strong {
  font-weight: 400;
  color: var(--t1);
  margin-left: 6px;
}
.asset-labels i {
  background: var(--t2);
}
.asset-labels span:last-child i {
  background: var(--bar);
}
.reliability-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
  margin: 22px 0;
}
.reliability-stats > div {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.reliability-stats strong {
  font-size: 32px;
  font-weight: 300;
  letter-spacing: -0.03em;
}
.reliability-stats span {
  font-size: 12px;
  color: var(--t3);
}
.insight-divider {
  display: flex;
  justify-content: space-between;
  border-top: 1px solid var(--line);
  padding-top: 14px;
  font-size: 13px;
  color: var(--t2);
}
.asset-retries {
  margin-top: 20px;
}
.insight-divider strong {
  font-weight: 400;
  color: var(--t1);
}
.error-bars {
  display: grid;
  gap: 16px;
}
.error-bars > div > div:first-child {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 7px;
  color: var(--t2);
  font-size: 12px;
}
.error-bars code {
  font-size: 12px;
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
  gap: 12px;
  font-size: 13px;
  color: var(--t3);
}
.chart-empty svg {
  color: var(--t3);
}
.source-ranking .analytics-panel-heading {
  padding: 22px 24px 0;
}
.source-ranking table {
  min-width: 740px;
}
.source-ranking th:first-child {
  width: 25%;
}
.source-ranking td:first-child strong,
.source-ranking td:first-child small {
  overflow-wrap: anywhere;
}
.ranking-count {
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: 140px;
}
.ranking-count strong {
  min-width: 24px;
  text-align: right;
}
.ranking-count > div {
  height: 2px;
  background: var(--line);
  flex: 1;
  min-width: 40px;
  overflow: hidden;
}
.ranking-count span {
  display: block;
  background: var(--t2);
  height: 100%;
}
.ranking-empty {
  padding: 32px;
  color: var(--t3);
}
.analytics-note {
  font-size: 12px;
  line-height: 1.9;
  color: var(--t3);
  margin-top: 16px;
}
.analytics-message {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 32px 0;
  border-top: 1px solid var(--line-strong);
  color: var(--t3);
}
.analytics-message.error {
  color: var(--red);
}
.analytics-empty {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px 0;
  border-bottom: 1px solid var(--line);
  margin-bottom: 24px;
  color: var(--t3);
}
.analytics-empty h3 {
  font-size: 14px;
  font-weight: 400;
}
.analytics-empty p {
  font-size: 13px;
  color: var(--t3);
  margin-top: 4px;
}
.analytics-empty .button {
  margin-left: auto;
}
.spinning {
  animation: analytics-spin 0.8s linear infinite;
}
@keyframes analytics-spin {
  to {
    transform: rotate(360deg);
  }
}
@media (max-width: 1250px) {
  .result-content {
    flex-direction: column;
    gap: 18px;
    min-height: 240px;
  }
  .result-legend {
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
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
    gap: 14px;
  }
  .analytics-updated {
    display: none;
  }
}
@media (max-width: 600px) {
  .analytics-tabs {
    gap: 20px;
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
    padding: 18px 16px;
  }
  .source-ranking .analytics-panel-heading {
    padding: 18px 16px 0;
  }
  .trend-panel .analytics-panel-heading {
    align-items: flex-start;
    flex-wrap: wrap;
  }
  .result-content {
    gap: 16px;
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
@media (prefers-reduced-motion: reduce) {
  .spinning {
    animation: none;
  }
}
</style>
