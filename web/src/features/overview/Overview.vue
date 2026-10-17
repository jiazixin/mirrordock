<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { CircleAlert, CirclePause, Package, RefreshCw, Server, Tag, Unplug } from '@lucide/vue'
import RunDetail from '../runs/RunDetail.vue'
import { api } from '../../app/api'
import { state, notify, perform, runAction, runSource } from '../../app/state'
import { bytes, duration, phases, sourceType } from '../../app/format'
import type { Run, Source } from '../../app/types'

const HOUR = 3_600_000
const PAST = 24 * HOUR
const SPAN = PAST + 12 * HOUR
const PAGE = 100
const MAX_PAGES = 5
const active = ['running', 'pausing']
const held = ['paused', 'interrupted']
const terminal = ['succeeded', 'failed', 'timed_out', 'cancelled']
const steps = ['准备', '传输', '校验', '发布']

const now = ref(Date.now())
const history = ref<Run[]>([])
const truncated = ref(false)
const detail = ref<number>()
let controller: AbortController | undefined
let request = 0
let failing = false
let timer: ReturnType<typeof setInterval> | undefined

async function load() {
  controller?.abort()
  const current = new AbortController()
  controller = current
  const id = ++request
  try {
    const items: Run[] = []
    let total = 0
    for (let page = 0; page < MAX_PAGES; page++) {
      const payload = await api<{ items: Run[]; total: number }>(
        `/runs?range=24h&limit=${PAGE}&offset=${page * PAGE}`,
        'GET',
        undefined,
        current.signal,
      )
      if (id !== request) return
      items.push(...payload.items)
      total = payload.total
      if (items.length >= total || payload.items.length < PAGE) break
    }
    history.value = items
    truncated.value = items.length < total
    failing = false
  } catch (error) {
    // Report the first failure of a streak only; the 30 s refresh keeps retrying quietly.
    if (id === request && !current.signal.aborted && !failing) {
      failing = true
      notify(error instanceof Error ? error.message : String(error), 'error')
    }
  }
}
function tick() {
  if (document.visibilityState !== 'visible') return
  refresh()
}
function refresh() {
  now.value = Date.now()
  void load()
}
const typeIcons: Record<string, typeof Server> = {
  rsync: Server,
  reposync: Package,
  github_release: Tag,
}
watch(
  () => state.revision,
  () => void load(),
  { immediate: true },
)
// A run that reaches a terminal state leaves the live list; reload so its bar keeps its end time.
watch(
  () =>
    state.runs
      .filter((run) => terminal.includes(run.state))
      .map((run) => run.id)
      .join(),
  (next, previous) => {
    if (previous !== undefined && next !== previous) void load()
  },
)
onMounted(() => {
  timer = setInterval(tick, 30_000)
  document.addEventListener('visibilitychange', tick)
})
onBeforeUnmount(() => {
  clearInterval(timer)
  document.removeEventListener('visibilitychange', tick)
  request++
  controller?.abort()
})

const offset = computed(() => (state.info?.utc_offset_seconds ?? 0) * 1000)
const start = computed(() => now.value - PAST)
const position = (t: number) => ((t - start.value) / SPAN) * 100
const clamp = (value: number) => Math.min(100, Math.max(0, value))
const shifted = (t: number) => new Date(t + offset.value).toISOString()
const clock = (t: number) => shifted(t).slice(11, 16)
const day = (t: number) => shifted(t).slice(5, 10).replace('-', '/')
function when(t: number) {
  const days = Math.round(
    (Date.parse(shifted(t).slice(0, 10)) - Date.parse(shifted(now.value).slice(0, 10))) /
      (24 * HOUR),
  )
  return days === 0 ? clock(t) : days === 1 ? `明天 ${clock(t)}` : `${day(t)} ${clock(t)}`
}
function open(run?: Run) {
  if (run) detail.value = run.id
}
function parse(value?: string) {
  if (!value || value.startsWith('0001')) return NaN
  return Date.parse(value)
}

const nowLeft = computed(() => position(now.value))
const grid = computed(() => {
  const step = 6 * HOUR
  const first = Math.ceil((start.value + offset.value) / step) * step - offset.value
  const lines = []
  for (let t = first; t < start.value + SPAN; t += step) {
    const left = position(t)
    lines.push({
      t,
      left,
      label: (t + offset.value) % (24 * HOUR) === 0 ? day(t) : clock(t),
      hidden: Math.abs(left - nowLeft.value) < 11,
    })
  }
  return lines
})

const runs = computed(() => {
  const merged = new Map<number, Run>()
  for (const run of history.value) merged.set(run.id, run)
  for (const run of state.runs) merged.set(run.id, run)
  for (const run of state.liveRuns) merged.set(run.id, run)
  return [...merged.values()]
})
const latest = computed(() => {
  const map = new Map<string, Run>()
  for (const run of runs.value) {
    const current = map.get(run.source_id)
    if (!current || run.id > current.id) map.set(run.source_id, run)
  }
  return map
})

const labels: Record<string, string> = {
  queued: '排队中',
  running: '运行中',
  pausing: '正在暂停',
  paused: '已暂停',
  interrupted: '中断',
  succeeded: '成功',
  failed: '失败',
  timed_out: '超时',
  cancelled: '已取消',
}
const finishedKind: Record<string, string> = {
  cancelled: 'cancel',
  failed: 'fail',
  timed_out: 'fail',
}
interface Segment {
  key: string
  left: number
  width: number
  kind: string
  run?: Run
  label?: string
}
function segments(run: Run): Segment[] {
  const created = parse(run.created_at)
  const startedAt = parse(run.started_at)
  const begin = Number.isNaN(startedAt) ? created : startedAt
  if (Number.isNaN(begin)) return []
  const ended = parse(run.ended_at)
  const elapsedEnd = begin + Math.max(0, run.elapsed_seconds) * 1000
  const describe = `#${run.id} ${labels[run.state] ?? run.state} · ${when(begin)} · ${duration(run.elapsed_seconds)}`
  const pieces: { from: number; to: number; kind: string; run?: Run }[] = []
  if (run.state === 'queued') pieces.push({ from: created, to: now.value, kind: 'queue', run })
  else if (active.includes(run.state)) pieces.push({ from: begin, to: now.value, kind: 'run', run })
  else if (held.includes(run.state)) {
    const stop = Math.min(now.value, elapsedEnd)
    pieces.push({ from: begin, to: stop, kind: 'held', run })
    pieces.push({ from: stop, to: now.value, kind: 'hold-line' })
  } else {
    const end = Number.isNaN(ended) ? elapsedEnd : ended
    pieces.push({ from: begin, to: end, kind: finishedKind[run.state] ?? 'ok', run })
  }
  return pieces.flatMap((piece, index) => {
    const left = clamp(position(piece.from))
    const right = clamp(position(piece.to))
    if (right <= 0 || left >= 100 || (right <= left && piece.kind === 'hold-line')) return []
    return [
      {
        key: `${run.id}-${index}`,
        left,
        width: Math.max(0, right - left),
        kind: piece.kind,
        run: piece.run,
        label: piece.run ? describe : undefined,
      },
    ]
  })
}

const sources = computed(() => state.config?.config.sources ?? [])
function upcoming(source: Source) {
  if (!source.enabled || !source.schedule.enabled) return undefined
  const t = parse(state.info?.schedules[source.id])
  return Number.isNaN(t) ? undefined : t
}
const lanes = computed(() =>
  sources.value.map((source) => {
    const next = upcoming(source)
    const last = latest.value.get(source.id)
    const skip = !!next && !!last && held.includes(last.state)
    return {
      source,
      segments: runs.value.filter((run) => run.source_id === source.id).flatMap(segments),
      tick: next !== undefined && position(next) <= 100 ? position(next) : undefined,
      skip,
      next:
        next === undefined
          ? !source.enabled
            ? '已停用'
            : '手动运行'
          : `${when(next)}${skip ? ' 将跳过' : ''}`,
    }
  }),
)

const live = computed(() => {
  const merged = new Map<number, Run>()
  for (const run of [...state.runs, ...state.liveRuns])
    if ([...active, 'queued'].includes(run.state)) merged.set(run.id, run)
  return [...merged.values()].sort(
    (a, b) => Number(b.state !== 'queued') - Number(a.state !== 'queued') || a.id - b.id,
  )
})
const running = computed(() => live.value.filter((run) => run.state !== 'queued'))
const queued = computed(() => live.value.filter((run) => run.state === 'queued'))
function step(run: Run) {
  if (['waiting_space', 'waiting_downloads'].includes(run.progress.phase)) return -1
  return (
    (
      { prepare: 0, scan: 0, transfer: 1, verify: 2, publish: 3, complete: 3 } as Record<
        string,
        number
      >
    )[run.progress.phase] ?? -1
  )
}
function received(run: Run) {
  return run.progress.received_bytes == null ? '未统计' : bytes(run.progress.received_bytes)
}

const attention = computed(() =>
  sources.value.flatMap((source) => {
    const run = latest.value.get(source.id)
    if (!run || ![...held, 'failed', 'timed_out'].includes(run.state)) return []
    return [{ source, run }]
  }),
)
function explain(run: Run) {
  if (run.state === 'paused')
    return `已用 ${duration(run.elapsed_seconds)}，预算 ${duration(run.budget_seconds)}。继续会沿用剩余预算与重试计数。`
  if (run.state === 'interrupted')
    return run.error || '任务中断，可继续执行。中断期间该源的计划触发会被跳过。'
  return [run.error_code, run.error].filter(Boolean).join(' · ') || '公开内容仍为上一发布代。'
}

const counts = computed(() => {
  const finished = history.value.filter((run) => terminal.includes(run.state))
  const succeeded = finished.filter((run) => run.state === 'succeeded').length
  const failed = finished.filter((run) => run.state === 'failed').length
  const timedOut = finished.filter((run) => run.state === 'timed_out').length
  return { succeeded, failed, timedOut, evaluated: succeeded + failed + timedOut }
})
const nextPlan = computed(
  () =>
    lanes.value
      .filter((lane) => lane.tick !== undefined && !lane.skip)
      .map((lane) => ({ name: lane.source.name, t: upcoming(lane.source)! }))
      .sort((a, b) => a.t - b.t)[0],
)
const headline = computed(() =>
  running.value.length
    ? `${running.value.length} 个任务运行中`
    : queued.value.length
      ? `${queued.value.length} 个任务排队中`
      : attention.value.length
        ? `${attention.value.length} 项需要处理`
        : '全部正常',
)
const tone = computed(() =>
  running.value.length
    ? 'live'
    : queued.value.length
      ? ''
      : attention.value.length
        ? attention.value.some((item) => !held.includes(item.run.state))
          ? 'bad'
          : 'warn'
        : 'good',
)
const kpis = computed(() => {
  const list = sources.value
  const { succeeded, failed, timedOut, evaluated } = counts.value
  return [
    {
      label: '同步源',
      value: String(list.length),
      unit: '个',
      foot: `${list.filter((s) => s.enabled).length} 个启用 · ${list.filter((s) => s.enabled && s.schedule.enabled).length} 个自动计划`,
    },
    {
      label: '进行中',
      value: String(running.value.length),
      unit: '个任务',
      foot: `${queued.value.length} 个排队 · ${attention.value.filter((a) => held.includes(a.run.state)).length} 个暂停或中断`,
    },
    {
      label: '24 小时成功率',
      value: evaluated ? ((100 * succeeded) / evaluated).toFixed(1) : '—',
      unit: evaluated ? '%' : '',
      foot: `${succeeded} 成功 · ${failed + timedOut} 失败或超时`,
    },
    {
      label: '本次启动分发',
      value: bytes(state.info?.downloads.bytes ?? 0),
      unit: '',
      foot: `${state.info?.downloads.active ?? 0} 条活动下载`,
    },
  ]
})
</script>

<template>
  <section class="overview">
    <div class="page-heading">
      <div>
        <h1>
          概览<span
            class="title-dot"
            aria-hidden="true"
            >.</span
          >
        </h1>
      </div>
      <button
        class="button"
        type="button"
        @click="refresh"
      >
        <RefreshCw :size="17" />刷新
      </button>
    </div>

    <header class="overview-hero">
      <div class="hero-line">
        <strong :class="tone">{{ headline }}</strong
        ><span>{{ day(now) }} {{ clock(now) }} · {{ state.info?.timezone ?? '—' }}</span>
      </div>
      <div class="hero-stats">
        <article
          v-for="item in kpis"
          :key="item.label"
        >
          <span>{{ item.label }}</span
          ><strong
            >{{ item.value }}<small v-if="item.unit">{{ item.unit }}</small></strong
          ><small>{{ item.foot }}</small>
        </article>
      </div>
      <p class="hero-note">
        {{
          live.length
            ? attention.length
              ? `另有 ${attention.length} 项需要处理。`
              : '没有需要处理的事项。'
            : '当前没有运行中的任务。'
        }}<template v-if="nextPlan"
          >下一次计划在 <b>{{ when(nextPlan.t) }}</b
          >，{{ nextPlan.name }}。</template
        >
      </p>
    </header>

    <section
      class="card"
      aria-labelledby="timeline-title"
    >
      <div class="card-head">
        <div>
          <h2 id="timeline-title">同步时间轴</h2>
          <small>过去 24 小时 · 未来 12 小时 · 每行一个同步源</small>
        </div>
        <div
          class="legend"
          aria-hidden="true"
        >
          <span><i class="swatch ok" />成功</span><span><i class="swatch run" />运行中</span
          ><span><i class="swatch fail" />失败 / 超时</span
          ><span><i class="swatch held" />暂停 / 中断</span
          ><span><i class="swatch plan" />计划</span
          ><span><i class="swatch plan skip" />将跳过</span>
        </div>
      </div>
      <div class="lanes-scroll">
        <div class="lanes">
          <div class="lane lane-head">
            <span>同步源</span>
            <div class="track">
              <span
                v-for="line in grid"
                v-show="!line.hidden"
                :key="line.t"
                class="hour-label"
                :style="{ left: `${line.left}%` }"
                >{{ line.label }}</span
              ><span
                class="hour-label now-label"
                :style="{ left: `${nowLeft}%` }"
                >现在 {{ clock(now) }}</span
              >
            </div>
            <span class="lane-next">下次计划</span>
          </div>
          <div
            v-for="lane in lanes"
            :key="lane.source.id"
            class="lane"
            :class="{ disabled: !lane.source.enabled }"
          >
            <div class="lane-name">
              <span
                class="type-icon"
                aria-hidden="true"
                ><component
                  :is="typeIcons[lane.source.type] ?? Server"
                  :size="17"
              /></span>
              <div>
                <strong>{{ lane.source.name }}</strong
                ><small>{{ sourceType(lane.source.type) }} · /{{ lane.source.dest }}/</small>
              </div>
            </div>
            <div class="track">
              <span
                class="future"
                :style="{ left: `${nowLeft}%` }"
              /><span
                v-for="line in grid"
                :key="line.t"
                class="gridline"
                :style="{ left: `${line.left}%` }"
              /><span
                class="now-line"
                :style="{ left: `${nowLeft}%` }"
              />
              <template
                v-for="segment in lane.segments"
                :key="segment.key"
              >
                <button
                  v-if="segment.run"
                  type="button"
                  class="segment"
                  :class="segment.kind"
                  :style="{ left: `${segment.left}%`, width: `${segment.width}%` }"
                  tabindex="-1"
                  :aria-label="`${lane.source.name} ${segment.label}`"
                  :title="segment.label"
                  @click="open(segment.run)"
                /><span
                  v-else
                  class="segment"
                  :class="segment.kind"
                  :style="{ left: `${segment.left}%`, width: `${segment.width}%` }"
                />
              </template>
              <span
                v-if="lane.tick !== undefined"
                class="tick"
                :class="{ skip: lane.skip }"
                :style="{ left: `${lane.tick}%` }"
                :title="lane.next"
              />
            </div>
            <span
              class="lane-next"
              :class="{ skip: lane.skip }"
              >{{ lane.next }}</span
            >
          </div>
          <p
            v-if="!lanes.length"
            class="lanes-empty"
          >
            还没有同步源。<RouterLink to="/sources">前往同步源</RouterLink>新增一个。
          </p>
        </div>
      </div>
      <small
        v-if="truncated"
        class="lanes-note"
        >近 24 小时任务较多，时间轴仅显示最近 {{ PAGE * MAX_PAGES }} 个。</small
      >
    </section>

    <div class="overview-columns">
      <section
        class="card"
        aria-labelledby="live-title"
      >
        <div class="card-head">
          <div>
            <h2 id="live-title">
              进行中<span
                v-if="live.length"
                class="count-pill"
                >{{ live.length }}</span
              >
            </h2>
            <small>准备、传输、校验、发布与重试等待共用累计预算</small>
          </div>
        </div>
        <article
          v-for="run in running"
          :key="run.id"
          class="live-run"
        >
          <div class="live-title">
            <div>
              <strong>{{ run.name }}</strong
              ><small
                >#{{ run.id }} · {{ run.trigger === 'schedule' ? '自动计划' : '手动触发' }}</small
              >
            </div>
            <span class="live-phase">{{ phases[run.progress.phase] ?? run.progress.phase }}</span>
          </div>
          <ol
            class="live-steps"
            aria-label="同步阶段"
          >
            <li
              v-for="(label, index) in steps"
              :key="label"
              :class="{ done: index < step(run), now: index === step(run) }"
              :aria-current="index === step(run) ? 'step' : undefined"
            >
              <i /><span>{{ label }}</span
              ><span
                v-if="index < step(run)"
                class="visually-hidden"
                >已完成</span
              >
            </li>
          </ol>
          <div class="live-figures">
            <div>
              <strong>{{ received(run) }}</strong
              ><small>实际接收</small>
            </div>
            <div>
              <strong>{{ duration(run.elapsed_seconds) }}</strong
              ><small>已运行</small>
            </div>
            <div>
              <strong>{{ duration(Math.max(0, run.budget_seconds - run.elapsed_seconds)) }}</strong
              ><small>预算剩余</small>
            </div>
            <div v-if="run.progress.percent !== null && run.progress.phase === 'transfer'">
              <strong>{{ run.progress.percent.toFixed(0) }}%</strong><small>进度</small>
            </div>
          </div>
          <div class="live-actions">
            <button
              v-if="run.state === 'running'"
              class="button compact tone-amber"
              @click="perform(() => runAction(run.id, 'pause'))"
            >
              暂停</button
            ><button
              class="button compact tone-red"
              @click="perform(() => runAction(run.id, 'cancel'))"
            >
              取消</button
            ><button
              class="button compact"
              @click="detail = run.id"
            >
              详情
            </button>
          </div>
        </article>
        <div
          v-for="run in queued"
          :key="run.id"
          class="queued-row"
        >
          <span class="badge queued"
            ><span
              class="badge-dot"
              aria-hidden="true"
            ></span
            >排队中</span
          ><strong>{{ run.name }}</strong
          ><small>#{{ run.id }} · {{ phases[run.progress.phase] ?? '等待空闲 worker' }}</small>
          <button
            class="button compact tone-red"
            @click="perform(() => runAction(run.id, 'cancel'))"
          >
            取消
          </button>
        </div>
        <p
          v-if="!live.length"
          class="quiet"
        >
          当前没有运行或排队的任务。
        </p>
      </section>

      <section
        class="card"
        aria-labelledby="attention-title"
      >
        <div class="card-head">
          <div>
            <h2 id="attention-title">需要处理</h2>
            <small>按各同步源最近一次任务</small>
          </div>
        </div>
        <article
          v-for="item in attention"
          :key="item.run.id"
          class="attention-row"
        >
          <span
            class="attention-icon"
            :class="item.run.state"
            aria-hidden="true"
            ><component
              :is="
                item.run.state === 'paused'
                  ? CirclePause
                  : item.run.state === 'interrupted'
                    ? Unplug
                    : CircleAlert
              "
              :size="18"
          /></span>
          <div>
            <div class="attention-title">
              <strong>{{ item.source.name }}</strong
              ><span
                class="badge"
                :class="item.run.state"
                >{{ labels[item.run.state] }}</span
              ><small>#{{ item.run.id }}</small>
            </div>
            <p>{{ explain(item.run) }}</p>
          </div>
          <div class="attention-actions">
            <template v-if="held.includes(item.run.state)">
              <button
                class="button compact primary"
                @click="perform(() => runAction(item.run.id, 'resume'))"
              >
                继续</button
              ><button
                class="button compact tone-red"
                @click="perform(() => runAction(item.run.id, 'cancel'))"
              >
                取消
              </button>
            </template>
            <template v-else>
              <button
                class="button compact primary"
                :disabled="!item.source.enabled"
                @click="perform(() => runSource(item.source.id))"
              >
                重新同步</button
              ><button
                class="button compact"
                @click="detail = item.run.id"
              >
                详情
              </button>
            </template>
          </div>
        </article>
        <p
          v-if="!attention.length"
          class="quiet"
        >
          各同步源最近一次任务均正常。
        </p>
        <div class="attention-footer">
          <small
            >最近 24 小时：{{ counts.succeeded }} 次成功 · {{ counts.failed }} 次失败 ·
            {{ counts.timedOut }} 次超时</small
          ><RouterLink to="/batches">全部批次</RouterLink>
        </div>
      </section>
    </div>
    <RunDetail
      v-if="detail"
      :id="detail"
      @close="detail = undefined"
    />
  </section>
</template>

<style scoped>
.overview {
  display: flex;
  flex-direction: column;
  gap: 16px;
}
.overview .page-heading {
  margin-bottom: 4px;
}
.overview-hero {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 20px 22px 18px;
  border: 1px solid var(--tint-line);
  border-radius: var(--radius);
  background: var(--tint);
}
.hero-line {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 14px;
}
.hero-line strong {
  font-size: 17px;
  font-weight: 700;
}
.hero-line strong.good {
  color: var(--ok);
}
.hero-line strong.live {
  color: var(--blue);
}
.hero-line strong.warn {
  color: var(--amb);
}
.hero-line strong.bad {
  color: var(--red);
}
.hero-line span {
  font-size: 13px;
  color: var(--t3);
}
.hero-stats {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  margin: 14px 0 12px;
}
.hero-stats article {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  padding: 4px 22px;
  border-left: 1px solid var(--tint-line);
}
.hero-stats article:first-child {
  padding-left: 0;
  border-left: 0;
}
.hero-stats article > span {
  font-size: 13.5px;
  color: var(--t3);
}
.hero-stats article > strong {
  font-size: 30px;
  font-weight: 650;
  letter-spacing: -0.02em;
  line-height: 1.2;
  overflow-wrap: anywhere;
}
.hero-stats strong small {
  margin-left: 5px;
  font-size: 14px;
  font-weight: 500;
  letter-spacing: 0;
  color: var(--t3);
}
.hero-note {
  font-size: 13.5px;
  color: var(--t2);
  padding-top: 12px;
  border-top: 1px solid var(--tint-line);
}
.hero-note b {
  font-weight: 600;
  color: var(--t1);
}
.card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--panel);
  overflow: hidden;
}
.card-head {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 10px 24px;
  padding: 20px 22px;
}
.card-head > div:first-child {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.card-head h2 {
  display: flex;
  align-items: center;
  gap: 8px;
}
.count-pill {
  display: inline-grid;
  place-items: center;
  min-width: 22px;
  height: 22px;
  padding: 0 7px;
  border-radius: 999px;
  background: var(--blue-bg);
  color: var(--blue);
  font-size: 12.5px;
  font-weight: 600;
}
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  font-size: 13px;
  color: var(--t2);
}
.legend span {
  display: inline-flex;
  align-items: center;
  gap: 7px;
}
.swatch {
  display: inline-block;
  width: 14px;
  height: 8px;
  border-radius: 3px;
}
.swatch.ok {
  background: var(--bar);
}
.swatch.run {
  background: var(--blue);
}
.swatch.fail {
  background: var(--red);
}
.swatch.held {
  background: var(--amb);
}
.swatch.plan {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  border: 2px solid var(--t3);
}
.swatch.plan.skip {
  border-color: var(--amb);
  border-style: dashed;
}
.lanes-scroll {
  overflow-x: auto;
  border-top: 1px solid var(--line);
}
.lanes {
  min-width: 900px;
}
.lane {
  display: grid;
  grid-template-columns: minmax(200px, 270px) minmax(0, 1fr) 140px;
  align-items: center;
  min-height: 68px;
  padding: 0 22px;
  border-bottom: 1px solid var(--line);
}
.lane:last-child {
  border-bottom: 0;
}
.lane-head {
  min-height: 46px;
  font-size: 13.5px;
  font-weight: 500;
  color: var(--t2);
  background: var(--head);
}
.lane-name {
  display: flex;
  align-items: center;
  gap: 12px;
  padding-right: 16px;
  min-width: 0;
}
.lane-name > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}
.type-icon {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  flex-shrink: 0;
  border: 1px solid var(--line);
  border-radius: 9px;
  background: var(--gray-bg);
  color: var(--t2);
}
.lane-name strong {
  font-size: 14.5px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.lane.disabled .lane-name strong {
  color: var(--t3);
}
.lane-name small {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.track {
  position: relative;
  height: 68px;
}
.lane-head .track {
  height: 46px;
}
.hour-label {
  position: absolute;
  top: 13px;
  transform: translateX(-50%);
  font-size: 12.5px;
  font-weight: 400;
  color: var(--t3);
  white-space: nowrap;
}
.now-label {
  top: 11px;
  padding: 1px 8px;
  border-radius: 999px;
  background: var(--brand);
  color: var(--brand-ink);
  font-weight: 600;
}
.future {
  position: absolute;
  top: 0;
  bottom: 0;
  right: 0;
  background: var(--future);
}
.gridline,
.now-line {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 1px;
  background: var(--line);
}
.now-line {
  width: 2px;
  background: var(--brand);
  opacity: 0.5;
}
.segment {
  position: absolute;
  top: 50%;
  height: 14px;
  min-width: 6px;
  margin: 0;
  padding: 0;
  border: 0;
  border-radius: 4px;
  transform: translateY(-50%);
  background: var(--bar);
}
button.segment:hover,
button.segment:focus-visible {
  outline: 2px solid var(--t1);
  outline-offset: 2px;
}
.segment.run {
  background: var(--blue);
  animation: live-pulse 2.4s ease-in-out infinite;
}
.segment.queue {
  background: var(--panel);
  box-shadow: inset 0 0 0 1.5px var(--t3);
}
.segment.fail {
  background: var(--red);
}
.segment.cancel {
  background: var(--line-strong);
}
.segment.held {
  background: var(--amb);
}
.segment.hold-line {
  height: 0;
  min-width: 0;
  border-top: 1.5px dashed var(--amb);
  border-radius: 0;
  background: transparent;
}
.tick {
  position: absolute;
  top: 50%;
  width: 11px;
  height: 11px;
  border-radius: 50%;
  border: 2px solid var(--t3);
  background: var(--panel);
  transform: translate(-50%, -50%);
}
.tick.skip {
  border-color: var(--amb);
  border-style: dashed;
}
.lane-next {
  text-align: right;
  font-size: 14px;
  font-weight: 600;
  color: var(--t1);
  white-space: nowrap;
}
.lane.disabled .lane-next {
  color: var(--t3);
  font-weight: 400;
}
.lane-head .lane-next {
  font-size: 13.5px;
  font-weight: 500;
  color: var(--t2);
}
.lane-next.skip {
  color: var(--amb);
}
.lanes-empty {
  padding: 32px 22px;
  color: var(--t3);
}
.lanes-empty a {
  color: var(--brand);
  font-weight: 600;
}
.lanes-note {
  padding: 12px 22px;
  border-top: 1px solid var(--line);
}
.overview-columns {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(440px, 100%), 1fr));
  gap: 16px;
  align-items: start;
}
.live-run {
  display: flex;
  flex-direction: column;
  gap: 18px;
  padding: 20px 22px 22px;
  border-top: 1px solid var(--line);
}
.live-title {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 16px;
}
.live-title > div {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 4px 12px;
  min-width: 0;
}
.live-title strong {
  font-size: 16px;
  font-weight: 650;
}
.live-phase {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 26px;
  padding: 0 10px;
  border: 1px solid var(--blue-bd);
  border-radius: 7px;
  background: var(--blue-bg);
  font-size: 13px;
  font-weight: 500;
  color: var(--blue);
  white-space: nowrap;
}
.live-phase::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  animation: live-pulse 2.4s ease-in-out infinite;
}
.live-steps {
  display: flex;
  gap: 6px;
  list-style: none;
  margin: 0;
  padding: 0;
}
.live-steps li {
  flex: 1 1 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  min-width: 0;
  font-size: 13px;
  color: var(--t3);
}
.live-steps i {
  display: block;
  height: 6px;
  border-radius: 3px;
  background: var(--gray-bg);
}
.live-steps .done {
  color: var(--ok);
}
.live-steps .done i {
  background: var(--bar);
}
.live-steps .now i {
  background: var(--blue);
}
.live-steps .now {
  color: var(--blue);
  font-weight: 600;
}
.live-figures {
  display: flex;
  flex-wrap: wrap;
  gap: 12px 32px;
}
.live-figures > div {
  display: flex;
  flex-direction: column;
}
.live-figures strong {
  font-size: 18px;
  font-weight: 650;
}
.live-actions {
  display: flex;
  gap: 8px;
}
.queued-row {
  display: grid;
  grid-template-columns: auto minmax(0, auto) minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
  padding: 16px 22px;
  border-top: 1px solid var(--line);
}
.queued-row strong {
  font-weight: 600;
}
.attention-row {
  display: grid;
  grid-template-columns: 38px minmax(0, 1fr) auto;
  gap: 4px 14px;
  padding: 18px 22px;
  border-top: 1px solid var(--line);
  align-items: start;
}
.attention-icon {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  border: 1px solid var(--red-bd);
  border-radius: 9px;
  background: var(--red-soft);
  color: var(--red);
}
.attention-icon.paused,
.attention-icon.interrupted {
  border-color: var(--amb-bd);
  background: var(--amb-soft);
  color: var(--amb);
}
.attention-title {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 10px;
}
.attention-title strong {
  font-size: 15px;
  font-weight: 600;
}
.attention-row p {
  margin-top: 4px;
  font-size: 13px;
  color: var(--t2);
  line-height: 1.6;
  overflow-wrap: anywhere;
}
.attention-actions {
  display: flex;
  gap: 8px;
}
.attention-footer {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 13px 22px;
  border-top: 1px solid var(--line);
  background: var(--head);
}
.attention-footer a {
  font-size: 13.5px;
  font-weight: 600;
  color: var(--brand);
}
.attention-footer a:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}
.quiet {
  padding: 22px;
  color: var(--t3);
  font-size: 13.5px;
  border-top: 1px solid var(--line);
}
@media (max-width: 900px) {
  .hero-stats {
    grid-template-columns: repeat(2, minmax(0, 1fr));
    row-gap: 14px;
  }
  .hero-stats article:nth-child(3) {
    padding-left: 0;
    border-left: 0;
  }
  .overview-columns {
    grid-template-columns: minmax(0, 1fr);
  }
}
@media (max-width: 600px) {
  .overview-hero {
    padding: 18px 16px 16px;
  }
  .hero-stats article {
    padding: 4px 14px;
  }
  .hero-stats article > strong {
    font-size: 24px;
  }
  .card-head,
  .live-run,
  .attention-row,
  .queued-row,
  .attention-footer {
    padding-left: 16px;
    padding-right: 16px;
  }
  .attention-row {
    grid-template-columns: 38px minmax(0, 1fr);
  }
  .attention-actions {
    grid-column: 2;
    margin-top: 8px;
  }
  .queued-row {
    grid-template-columns: auto minmax(0, 1fr) auto;
  }
  .queued-row small {
    grid-column: 2 / -1;
  }
}
@media (max-width: 360px) {
  .hero-stats {
    grid-template-columns: minmax(0, 1fr);
  }
  .hero-stats article,
  .hero-stats article:nth-child(3) {
    padding-left: 0;
    border-left: 0;
  }
}
</style>
