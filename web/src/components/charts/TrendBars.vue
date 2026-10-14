<script setup lang="ts">
import { computed, ref } from 'vue'
import { formatBytes, niceBytesScale } from '../../composables/format'
import { useWidth } from '../../composables/size'
import type { TrendSeries } from './types'

const props = withDefaults(
  defineProps<{
    labels: string[] // full bucket labels (tooltip)
    ticks: string[] // short axis labels, same length
    series: TrendSeries[] // stacked bottom-up in this order
    // values: null where the comparison period has no data (before collection began)
    compare?: { name: string; labels: string[]; values: (number | null)[] } | null
    totalLabel: string
    changeLabel: string
    height?: number
  }>(),
  { compare: null, height: 280 },
)

const host = ref<HTMLElement>()
const width = useWidth(host)
const M = { l: 64, r: 12, t: 12, b: 26 }
const n = computed(() => props.labels.length)

const totals = computed(() => props.labels.map((_, i) => props.series.reduce((s, x) => s + (x.values[i] ?? 0), 0)))
const scale = computed(() =>
  niceBytesScale(Math.max(1, ...totals.value, ...(props.compare?.values.slice(0, n.value).map((v) => v ?? 0) ?? []))),
)

const iw = computed(() => Math.max(0, width.value - M.l - M.r))
const ih = computed(() => props.height - M.t - M.b)
const step = computed(() => (n.value ? iw.value / n.value : 0))
const barW = computed(() => Math.max(1, Math.min(48, step.value * (n.value > 60 ? 0.8 : 0.66))))
const cx = (i: number) => M.l + step.value * (i + 0.5)
const y = (v: number) => M.t + ih.value - (v / scale.value.top) * ih.value

const yTicks = computed(() => {
  const out: number[] = []
  for (let v = 0; v <= scale.value.top + 1e-9; v += scale.value.step) out.push(v)
  return out
})
// Roughly one axis label per 72px.
const xEvery = computed(() => Math.max(1, Math.ceil(n.value / Math.max(1, Math.floor(iw.value / 72)))))

// Stacked segments with a 2px surface gap between them; only the top of a
// column is rounded, so the stack reads as one bar anchored to the baseline.
const GAP = 2
const R = 4
const columns = computed(() =>
  props.labels.map((_, i) => {
    let acc = 0
    const segs: { key: string; color: string; y: number; h: number }[] = []
    for (const s of props.series) {
      const v = s.values[i] ?? 0
      if (v <= 0) continue
      const y0 = y(acc)
      acc += v
      const y1 = y(acc)
      segs.push({ key: s.key, color: s.color, y: y1, h: y0 - y1 })
    }
    // Gaps come off the top of each lower segment; drop slivers they swallow.
    const out = segs
      .map((sg, j) => (j < segs.length - 1 ? { ...sg, y: sg.y + GAP, h: sg.h - GAP } : sg))
      .filter((sg) => sg.h >= 0.75)
    return out
  }),
)

function segPath(x: number, w: number, top: number, h: number, rounded: boolean) {
  const r = rounded ? Math.min(R, w / 2, h) : 0
  const b = top + h
  return `M${x},${b}V${top + r}${r ? `Q${x},${top} ${x + r},${top}` : ''}H${x + w - r}${r ? `Q${x + w},${top} ${x + w},${top + r}` : ''}V${b}Z`
}

const cmpPath = computed(() => {
  const c = props.compare
  if (!c) return ''
  // A bucket without comparison data breaks the line rather than reading as zero.
  return c.values
    .slice(0, n.value)
    .map((v, i, all) => (v === null ? '' : `${i && all[i - 1] !== null ? 'L' : 'M'}${cx(i).toFixed(1)},${y(v).toFixed(1)}`))
    .join('')
})

const hover = ref<number | null>(null)
function onMove(e: PointerEvent) {
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const i = Math.floor((e.clientX - rect.left) / (step.value || 1))
  hover.value = i >= 0 && i < n.value ? i : null
}
const tip = computed(() => {
  const i = hover.value
  if (i === null) return null
  const rows = props.series
    .map((s) => ({ name: s.name, color: s.color, v: s.values[i] ?? 0 }))
    .filter((r) => r.v > 0)
    .reverse() // top of the stack first, like the bars
  const cmp = props.compare?.values[i] ?? null
  const change = cmp && cmp > 0 ? (totals.value[i]! - cmp) / cmp : null
  return { i, label: props.labels[i], rows, total: totals.value[i]!, cmp, cmpLabel: props.compare?.labels[i], change }
})
const tipLeft = computed(() => {
  if (tip.value === null) return 0
  const x = M.l + step.value * (tip.value.i + 1)
  return x > width.value - 230 ? Math.max(0, M.l + step.value * tip.value.i - 220) : x + 8
})
const pct = (v: number) => `${v > 0 ? '+' : ''}${(v * 100).toFixed(v > -0.1 && v < 0.1 ? 1 : 0)}%`
</script>

<template>
  <div ref="host" class="trend">
    <svg v-if="width && n" :width="width" :height="height" role="img" :aria-label="totalLabel">
      <line v-for="v in yTicks" :key="'g' + v" :x1="M.l" :x2="width - M.r" :y1="y(v)" :y2="y(v)"
        :stroke="v === 0 ? 'var(--viz-axis)' : 'var(--viz-grid)'" stroke-width="1" />
      <text v-for="v in yTicks" :key="'y' + v" :x="M.l - 8" :y="y(v) + 4" text-anchor="end" class="tick">{{ formatBytes(v) }}</text>
      <template v-for="(t, i) in ticks" :key="'x' + i">
        <text v-if="i % xEvery === 0" :x="cx(i)" :y="height - 8" text-anchor="middle" class="tick">{{ t }}</text>
      </template>
      <rect v-if="hover !== null" :x="M.l + step * hover" :y="M.t" :width="step" :height="ih" class="hover-band" />
      <g v-for="(col, i) in columns" :key="'c' + i">
        <path v-for="(sg, j) in col" :key="sg.key" :d="segPath(cx(i) - barW / 2, barW, sg.y, sg.h, j === col.length - 1)" :fill="sg.color" />
      </g>
      <template v-if="compare">
        <path :d="cmpPath" fill="none" stroke="var(--viz-cmp)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
        <circle v-if="hover !== null && compare.values[hover] != null" :cx="cx(hover)" :cy="y(compare.values[hover]!)" r="4"
          fill="var(--viz-cmp)" stroke="var(--viz-surface)" stroke-width="2" />
      </template>
      <rect :x="M.l" :y="M.t" :width="iw" :height="ih" fill="transparent" @pointermove="onMove" @pointerleave="hover = null" />
    </svg>
    <div v-if="tip" class="viz-tooltip" :style="{ left: tipLeft + 'px', top: M.t + 8 + 'px' }">
      <div class="tt-title">{{ tip.label }}</div>
      <div v-for="r in tip.rows" :key="r.name" class="tt-row">
        <i class="viz-key sq" :style="{ background: r.color }" />{{ r.name }}<b>{{ formatBytes(r.v) }}</b>
      </div>
      <div class="tt-row total">{{ totalLabel }}<b>{{ formatBytes(tip.total) }}</b></div>
      <template v-if="tip.cmp !== null">
        <div class="tt-row"><i class="viz-key" style="background: var(--viz-cmp)" />{{ compare!.name }} · {{ tip.cmpLabel }}<b>{{ formatBytes(tip.cmp) }}</b></div>
        <div v-if="tip.change !== null" class="tt-row">{{ changeLabel }}<b>{{ pct(tip.change) }}</b></div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.trend {
  position: relative;
}
svg {
  display: block;
  touch-action: pan-y;
}
.tick {
  fill: var(--viz-muted);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
.hover-band {
  fill: var(--viz-grid);
  opacity: 0.6;
}
.viz-key.sq {
  width: 8px;
  height: 8px;
  border-radius: 2px;
}
.tt-row.total {
  border-top: 1px solid var(--viz-tooltip-border);
  margin-top: 2px;
  padding-top: 2px;
}
</style>
