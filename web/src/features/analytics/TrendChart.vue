<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import type { AnalyticsDay } from '../../app/types'
import { bytes, duration } from '../../app/format'
const props = defineProps<{ days: AnalyticsDay[]; metric: 'runs' | 'bytes' | 'duration' }>()
const hovered = ref<number | null>(null)
const container = ref<HTMLElement>()
const width = ref(800),
  height = 240,
  left = 65,
  right = 12,
  top = 18,
  bottom = 35
const plotWidth = computed(() => width.value - left - right),
  plotHeight = height - top - bottom
let observer: ResizeObserver | undefined
onMounted(() => {
  observer = new ResizeObserver((entries) => {
    width.value = Math.max(240, entries[0]?.contentRect.width ?? 800)
  })
  if (container.value) observer.observe(container.value)
})
onBeforeUnmount(() => observer?.disconnect())
const value = (day: AnalyticsDay) =>
  props.metric === 'runs'
    ? day.runs
    : props.metric === 'bytes'
      ? day.bytes
      : day.duration_samples
        ? day.elapsed_seconds / day.duration_samples
        : 0
// Tick steps are whole units (bytes in 1024 powers, durations in s/m/h) so
// every axis label is a readable value instead of a fraction of a byte.
const byteSteps = [0, 1, 2, 3, 4].flatMap((power) =>
  [1, 2, 5, 10, 20, 50, 100, 256, 512].map((n) => n * 1024 ** power),
)
const durationSteps = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800].concat(
  [1, 2, 3, 6, 12, 24, 48, 120, 240].map((hours) => hours * 3600),
)
const maximum = computed(() => {
  const max = Math.max(0, ...props.days.map(value))
  if (props.metric === 'runs') return Math.max(4, Math.ceil(max / 4) * 4)
  const steps = props.metric === 'bytes' ? byteSteps : durationSteps
  const step = steps.find((candidate) => candidate * 4 >= max) ?? Math.ceil(max / 4)
  return step * 4
})
const x = (i: number) => left + ((i + 0.5) * plotWidth.value) / props.days.length
const y = (n: number) => top + plotHeight * (1 - n / maximum.value)
const format = (n: number) =>
  props.metric === 'bytes'
    ? bytes(n)
    : props.metric === 'duration'
      ? duration(n)
      : n.toLocaleString('zh-CN', { maximumFractionDigits: 1 })
const ticks = computed(() => Array.from({ length: 5 }, (_, i) => (maximum.value * i) / 4))
const tickLabel = (n: number) =>
  props.metric === 'bytes'
    ? bytes(n).replace('.0 ', ' ')
    : props.metric === 'duration' && n >= 60
      ? n % 3600 === 0
        ? `${n / 3600}h`
        : n < 3600 && n % 60 === 0
          ? `${n / 60}m`
          : duration(n)
      : format(n)
const path = computed(() =>
  props.days.map((d, i) => `${i ? 'L' : 'M'}${x(i)},${y(value(d))}`).join(' '),
)
const labelIndices = computed(() => [
  ...new Set([
    0,
    Math.floor((props.days.length - 1) / 3),
    Math.floor(((props.days.length - 1) * 2) / 3),
    props.days.length - 1,
  ]),
])
const active = computed(() => (hovered.value === null ? null : props.days[hovered.value]))
const describe = (d: AnalyticsDay) =>
  `${d.date}：${props.metric === 'runs' ? `成功 ${d.succeeded}，失败 ${d.failed}，取消 ${d.cancelled}，未结束 ${d.active}` : props.metric === 'duration' && !d.duration_samples ? '无耗时样本' : format(value(d))}`
const metricLabel = computed(() =>
  props.metric === 'runs'
    ? '运行次数'
    : props.metric === 'bytes'
      ? '成功任务处理量'
      : '平均累计执行耗时',
)
</script>
<template>
  <div
    ref="container"
    class="analytics-trend"
  >
    <div
      class="trend-readout"
      aria-live="polite"
    >
      <span>{{ active?.date ?? '按日汇总' }}</span
      ><strong>{{ active ? describe(active).split('：')[1] : metricLabel }}</strong>
    </div>
    <svg
      :viewBox="`0 0 ${width} ${height}`"
      role="img"
      :aria-label="`${metricLabel}趋势，可聚焦各日期查看数据`"
    >
      <g
        v-for="tick in ticks"
        :key="tick"
      >
        <line
          :x1="left"
          :x2="width - right"
          :y1="y(tick)"
          :y2="y(tick)"
          class="chart-grid"
        />
        <text
          :x="left - 10"
          :y="y(tick) + 4"
          text-anchor="end"
          class="chart-text"
        >
          {{ tickLabel(tick) }}
        </text>
      </g>
      <template v-if="metric === 'runs'">
        <g
          v-for="(day, i) in days"
          :key="day.date"
        >
          <rect
            :x="x(i) - (plotWidth / days.length) * 0.28"
            :y="y(day.succeeded)"
            :width="(plotWidth / days.length) * 0.56"
            :height="(plotHeight * day.succeeded) / maximum"
            class="bar-ok"
          />
          <rect
            :x="x(i) - (plotWidth / days.length) * 0.28"
            :y="y(day.succeeded + day.failed)"
            :width="(plotWidth / days.length) * 0.56"
            :height="(plotHeight * day.failed) / maximum"
            class="bar-fail"
          />
          <rect
            :x="x(i) - (plotWidth / days.length) * 0.28"
            :y="y(day.succeeded + day.failed + day.cancelled)"
            :width="(plotWidth / days.length) * 0.56"
            :height="(plotHeight * day.cancelled) / maximum"
            class="bar-cancel"
          />
          <rect
            :x="x(i) - (plotWidth / days.length) * 0.28"
            :y="y(day.runs)"
            :width="(plotWidth / days.length) * 0.56"
            :height="(plotHeight * day.active) / maximum"
            class="bar-active"
          />
        </g>
      </template>
      <template v-else>
        <path
          :d="`${path} L${x(days.length - 1)},${y(0)} L${x(0)},${y(0)} Z`"
          class="area"
        />
        <path
          :d="path"
          class="line"
        />
      </template>
      <g
        v-for="(day, i) in days"
        :key="`hit-${day.date}`"
        tabindex="0"
        :aria-label="describe(day)"
        class="chart-hit"
        @mouseenter="hovered = i"
        @mouseleave="hovered = null"
        @focus="hovered = i"
        @blur="hovered = null"
      >
        <title>{{ describe(day) }}</title>
        <rect
          :x="left + (i * plotWidth) / days.length"
          :y="top"
          :width="plotWidth / days.length"
          :height="plotHeight"
          fill="transparent"
        />
        <line
          v-if="hovered === i"
          :x1="x(i)"
          :x2="x(i)"
          :y1="top"
          :y2="y(0)"
          class="cursor"
        />
        <circle
          v-if="hovered === i && metric !== 'runs'"
          :cx="x(i)"
          :cy="y(value(day))"
          r="3.5"
          class="dot"
        />
      </g>
      <text
        v-for="i in labelIndices"
        :key="`label-${i}`"
        :x="x(i)"
        :y="height - 10"
        text-anchor="middle"
        class="chart-text"
      >
        {{ days[i]?.date.slice(5).replace('-', '/') }}
      </text>
    </svg>
  </div>
</template>
<style scoped>
.analytics-trend {
  min-width: 0;
}
.analytics-trend svg {
  display: block;
  width: 100%;
  height: auto;
}
.trend-readout {
  display: flex;
  gap: 12px;
  align-items: center;
  min-height: 32px;
  color: var(--t3);
  font-size: 12px;
}
.trend-readout strong {
  font-weight: 400;
  color: var(--t1);
}
.chart-grid {
  stroke: var(--line);
  stroke-width: 1;
}
.chart-text {
  font-size: 11px;
  fill: var(--t3);
  font-family: inherit;
}
.bar-ok {
  fill: var(--chart-ok);
}
.bar-fail {
  fill: var(--red);
}
.bar-cancel {
  fill: var(--line-strong);
}
.bar-active {
  fill: var(--amb);
}
.area {
  fill: var(--acc-soft);
}
.line {
  fill: none;
  stroke: var(--acc);
  stroke-width: 1.5;
}
.cursor {
  stroke: var(--t3);
  stroke-dasharray: 3 4;
}
.dot {
  fill: var(--acc);
}
.chart-hit:focus {
  outline: none;
}
.chart-hit:focus > rect {
  stroke: var(--t1);
  stroke-width: 1;
}
@media (max-width: 480px) {
  .analytics-trend svg {
    min-height: 170px;
  }
  .trend-readout {
    flex-wrap: wrap;
    gap: 4px 10px;
  }
}
</style>
