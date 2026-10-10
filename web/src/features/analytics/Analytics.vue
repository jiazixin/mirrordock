<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import {
  Activity,
  CheckCircle2,
  Clock3,
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
        { label: '成功', count: data.value.summary.succeeded, color: '#315bea' },
        { label: '失败 / 超时', count: data.value.summary.failed, color: '#d66772' },
        { label: '已取消', count: data.value.summary.cancelled, color: '#a8b0bd' },
        { label: '未结束', count: data.value.summary.active, color: '#e8b15b' },
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
      <h1>{{ kindInfo.title }}<span>.</span></h1>
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
          <span>运行次数</span><Activity :size="18" /><strong>{{
            data.summary.runs.toLocaleString()
          }}</strong
          ><small>{{ data.summary.active }} 次未结束 · {{ data.summary.cancelled }} 次取消</small>
        </article>
        <article>
          <span>运行成功率</span><CheckCircle2 :size="18" /><strong>{{ rate(data.summary) }}</strong
          ><small
            >{{ data.summary.succeeded }} 成功 / {{ evaluated(data.summary) }} 次有效结果</small
          >
        </article>
        <article>
          <span>平均执行耗时</span><Clock3 :size="18" /><strong>{{ average(data.summary) }}</strong
          ><small>{{ data.summary.duration_samples }} 个样本 · 不含排队与暂停</small>
        </article>
        <article v-if="reposync">
          <span>自动重试任务</span><RefreshCw :size="18" /><strong>{{
            data.summary.retried_runs.toLocaleString()
          }}</strong
          ><small>期间发生过自动重试的任务数</small>
        </article>
        <article v-else>
          <span>成功任务处理量</span><ArrowDownToLine :size="18" /><strong class="bytes-stat">{{
            bytes(data.summary.bytes)
          }}</strong
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
.analytics-filters {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 20px;
}
.analytics-filters > .select-field:first-child {
  width: 150px;
}
.analytics-filters > .select-field:nth-child(2) {
  width: 210px;
}
.analytics-updated {
  margin-left: auto;
  font-size: 11px;
  color: var(--muted);
}
.analytics-stats article > small {
  display: block;
  margin-top: 10px;
  font-size: 11px;
}
.analytics-stats article > strong {
  font-size: 28px;
}
.analytics-stats article > strong.bytes-stat {
  font-size: 25px;
}
.analytics-grid {
  display: grid;
  grid-template-columns: minmax(0, 1.65fr) minmax(0, 1fr);
  gap: 18px;
  margin-bottom: 20px;
}
.analytics-grid > .panel {
  min-width: 0;
  padding: 20px;
}
.analytics-panel-heading {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  margin-bottom: 18px;
}
.analytics-panel-heading h3 {
  font-size: 14px;
}
.analytics-panel-heading small {
  font-size: 11px;
}
.analytics-panel-heading > svg {
  color: #8995a7;
}
.chart-tabs {
  display: flex;
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 3px;
  gap: 2px;
}
.chart-tabs button {
  background: none;
  border: 0;
  padding: 4px 10px;
  color: var(--muted);
  font-size: 11px;
  border-radius: 4px;
  white-space: nowrap;
}
.chart-tabs button[aria-pressed='true'] {
  background: var(--blue-light);
  color: var(--blue);
  font-weight: 500;
}
.chart-legend {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  justify-content: center;
  margin-top: 4px;
  font-size: 11px;
  color: #707b8e;
}
.chart-legend span {
  display: flex;
  gap: 6px;
  align-items: center;
}
i {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 2px;
  flex-shrink: 0;
}
.chart-footnote {
  font-size: 11px;
  color: var(--muted);
  line-height: 1.8;
  margin-top: 18px;
}
.result-content {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 24px;
  min-height: 210px;
}
.result-ring {
  width: 148px;
  height: 148px;
  flex-shrink: 0;
  border-radius: 50%;
  padding: 15px;
}
.result-ring.empty {
  background: #edf0f5;
}
.result-ring > div {
  height: 100%;
  width: 100%;
  border-radius: 50%;
  background: white;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}
.result-ring strong {
  font-size: 25px;
  font-weight: 500;
  letter-spacing: -0.6px;
}
.result-ring small {
  font-size: 11px;
  margin-top: 4px;
}
.result-legend {
  display: grid;
  gap: 15px;
  min-width: 0;
  font-size: 11px;
}
.result-legend > div {
  display: flex;
  align-items: center;
  gap: 7px;
  color: #757f8e;
}
.result-legend strong {
  margin-left: auto;
  color: #394255;
  font-size: 12px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.insight-total {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin: 24px 0;
}
.insight-total strong {
  font-size: 30px;
  font-weight: 500;
}
.insight-total span {
  font-size: 12px;
  color: var(--muted);
}
.asset-track {
  display: flex;
  height: 16px;
  border-radius: 4px;
  overflow: hidden;
  background: #f1f3f7;
}
.asset-track > span:first-child {
  background: var(--blue);
}
.asset-track > span:last-child {
  background: #afa1d8;
}
.asset-labels {
  display: flex;
  justify-content: space-between;
  gap: 15px;
  flex-wrap: wrap;
  font-size: 12px;
  color: #707a8d;
  margin-top: 13px;
}
.asset-labels span {
  display: flex;
  align-items: center;
  gap: 6px;
}
.asset-labels strong {
  font-weight: 500;
  color: #353f51;
  margin-left: 8px;
}
.asset-labels i {
  background: var(--blue);
}
.asset-labels span:last-child i {
  background: #afa1d8;
}
.reliability-stats {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 15px;
  margin: 22px 0;
}
.reliability-stats > div {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.reliability-stats strong {
  font-size: 28px;
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}
.reliability-stats span {
  font-size: 11px;
  color: var(--muted);
}
.insight-divider {
  display: flex;
  justify-content: space-between;
  border-top: 1px solid var(--line);
  padding-top: 16px;
  font-size: 12px;
  color: #707b8c;
}
.asset-retries {
  margin-top: 20px;
}
.insight-divider strong {
  font-weight: 500;
  color: #394255;
}
.error-bars {
  display: grid;
  gap: 15px;
}
.error-bars > div > div:first-child {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 7px;
  color: #7e8592;
  font-size: 11px;
}
.error-bars code {
  font-size: 11px;
  overflow-wrap: anywhere;
  min-width: 0;
}
.error-bars strong {
  font-weight: 500;
  white-space: nowrap;
}
.error-track {
  height: 5px;
  background: #f6eff1;
  border-radius: 3px;
  overflow: hidden;
}
.error-track span {
  display: block;
  height: 100%;
  background: #d66772;
  border-radius: 3px;
}
.chart-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  min-height: 135px;
  gap: 12px;
  font-size: 12px;
  color: #929aaa;
}
.chart-empty svg {
  color: #68a487;
}
.source-ranking .analytics-panel-heading {
  padding: 20px 20px 0;
}
.source-ranking table {
  min-width: 740px;
}
.source-ranking th:first-child {
  width: 25%;
}
.source-ranking td:first-child strong {
  overflow-wrap: anywhere;
}
.source-ranking td:first-child small {
  overflow-wrap: anywhere;
}
.ranking-count {
  display: flex;
  align-items: center;
  gap: 10px;
  max-width: 125px;
  margin: auto;
}
.ranking-count strong {
  min-width: 20px;
  text-align: right;
}
.ranking-count > div {
  height: 5px;
  background: #eef1f7;
  flex: 1;
  border-radius: 3px;
  min-width: 40px;
  overflow: hidden;
}
.ranking-count span {
  display: block;
  background: #6f8bea;
  height: 100%;
  border-radius: 3px;
}
.ranking-empty {
  padding: 32px;
  color: var(--muted);
}
.analytics-note {
  font-size: 11px;
  line-height: 1.9;
  color: #8b929d;
  margin-top: 16px;
}
.analytics-message {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 32px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  color: var(--muted);
}
.analytics-message.error {
  color: #bf5363;
  background: #fff9fa;
}
.analytics-empty {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  background: #f9fbff;
  border: 1px solid #e2e9f8;
  border-radius: var(--radius);
  margin-bottom: 18px;
  color: #8491ad;
}
.analytics-empty h3 {
  font-size: 13px;
}
.analytics-empty p {
  font-size: 12px;
  color: var(--muted);
  margin-top: 5px;
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
  }
  .result-legend {
    width: 100%;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 12px;
  }
  .result-content {
    min-height: 240px;
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
    gap: 15px;
  }
  .analytics-updated {
    display: none;
  }
}
@media (max-width: 600px) {
  .analytics-filters {
    flex-wrap: wrap;
  }
  .analytics-filters > .select-field:first-child,
  .analytics-filters > .select-field:nth-child(2) {
    flex: 1;
    min-width: 120px;
    width: auto;
  }
  .analytics-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .analytics-stats article {
    padding: 16px;
  }
  .analytics-stats article > svg {
    display: none;
  }
  .analytics-stats article > strong {
    font-size: 24px;
  }
  .analytics-stats article > strong.bytes-stat {
    font-size: 21px;
  }
  .analytics-grid > .panel {
    padding: 16px;
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
  .analytics-page .page-heading h1 {
    font-size: 24px;
  }
  .analytics-page .page-heading {
    gap: 10px;
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
