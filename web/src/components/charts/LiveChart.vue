<script setup lang="ts">
import { computed, ref } from 'vue'
import { formatRate, niceBytesScale } from '../../composables/format'
import { useWidth } from '../../composables/size'

interface Point {
  t: number // unix ms
  up: number
  down: number
}
const props = withDefaults(defineProps<{ samples: Point[]; labels: { down: string; up: string }; windowSec?: number; height?: number }>(), {
  windowSec: 300,
  height: 240,
})

const host = ref<HTMLElement>()
const width = useWidth(host)
const M = { l: 64, r: 12, t: 10, b: 24 }

const now = computed(() => props.samples.at(-1)?.t ?? Date.now())
const t0 = computed(() => now.value - props.windowSec * 1000)
const visible = computed(() => props.samples.filter((s) => s.t >= t0.value))
const scale = computed(() => niceBytesScale(Math.max(1, ...visible.value.map((s) => Math.max(s.up, s.down)))))

const iw = computed(() => Math.max(0, width.value - M.l - M.r))
const ih = computed(() => props.height - M.t - M.b)
const x = (t: number) => M.l + ((t - t0.value) / (props.windowSec * 1000)) * iw.value
const y = (v: number) => M.t + ih.value - (v / scale.value.top) * ih.value

const yTicks = computed(() => {
  const out: number[] = []
  for (let v = 0; v <= scale.value.top + 1e-9; v += scale.value.step) out.push(v)
  return out
})
const xTicks = computed(() => {
  const out: number[] = []
  const first = Math.ceil(t0.value / 60000) * 60000
  for (let t = first; t <= now.value; t += 60000) out.push(t)
  return out
})
const clock = (t: number, seconds = false) =>
  new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: seconds ? '2-digit' : undefined, hour12: false })

// A gap of more than a few seconds (tab in background, reconnect, sing-box
// restart) breaks the line instead of drawing a slope across missing data.
const GAP_MS = 3500
const path = (key: 'up' | 'down') =>
  visible.value
    .map((s, i, all) => {
      const jump = i === 0 || s.t - all[i - 1]!.t > GAP_MS
      return `${jump ? 'M' : 'L'}${x(s.t).toFixed(1)},${y(s[key]).toFixed(1)}`
    })
    .join('')

const hover = ref<Point | null>(null)
function onMove(e: PointerEvent) {
  const rect = (e.currentTarget as SVGElement).getBoundingClientRect()
  const px = e.clientX - rect.left
  let best: Point | null = null
  let bestD = Infinity
  for (const s of visible.value) {
    const d = Math.abs(x(s.t) - px)
    if (d < bestD) {
      bestD = d
      best = s
    }
  }
  hover.value = best
}
const tipLeft = computed(() => {
  if (!hover.value) return 0
  const hx = x(hover.value.t)
  return hx > width.value - 180 ? hx - 170 : hx + 12
})
</script>

<template>
  <div ref="host" class="live">
    <div class="viz-legend">
      <span><i class="viz-key" style="background: var(--viz-down)" />{{ labels.down }}</span>
      <span><i class="viz-key" style="background: var(--viz-up)" />{{ labels.up }}</span>
    </div>
    <svg v-if="width" :width="width" :height="height" role="img" :aria-label="`${labels.down} / ${labels.up}`">
      <g>
        <line v-for="v in yTicks" :key="'g' + v" :x1="M.l" :x2="width - M.r" :y1="y(v)" :y2="y(v)"
          :stroke="v === 0 ? 'var(--viz-axis)' : 'var(--viz-grid)'" stroke-width="1" />
        <text v-for="v in yTicks" :key="'y' + v" :x="M.l - 8" :y="y(v) + 4" text-anchor="end" class="tick">{{ formatRate(v) }}</text>
        <text v-for="t in xTicks" :key="'x' + t" :x="x(t)" :y="height - 6" text-anchor="middle" class="tick">{{ clock(t) }}</text>
      </g>
      <path :d="path('down')" fill="none" stroke="var(--viz-down)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
      <path :d="path('up')" fill="none" stroke="var(--viz-up)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
      <g v-if="hover">
        <line :x1="x(hover.t)" :x2="x(hover.t)" :y1="M.t" :y2="M.t + ih" stroke="var(--viz-muted)" stroke-width="1" />
        <circle :cx="x(hover.t)" :cy="y(hover.down)" r="4" fill="var(--viz-down)" stroke="var(--viz-surface)" stroke-width="2" />
        <circle :cx="x(hover.t)" :cy="y(hover.up)" r="4" fill="var(--viz-up)" stroke="var(--viz-surface)" stroke-width="2" />
      </g>
      <rect :x="M.l" :y="M.t" :width="iw" :height="ih" fill="transparent" @pointermove="onMove" @pointerleave="hover = null" />
    </svg>
    <div v-if="hover" class="viz-tooltip" :style="{ left: tipLeft + 'px', top: M.t + 24 + 'px' }">
      <div class="tt-title">{{ clock(hover.t, true) }}</div>
      <div class="tt-row"><i class="viz-key" style="background: var(--viz-down)" />{{ labels.down }}<b>{{ formatRate(hover.down) }}</b></div>
      <div class="tt-row"><i class="viz-key" style="background: var(--viz-up)" />{{ labels.up }}<b>{{ formatRate(hover.up) }}</b></div>
    </div>
  </div>
</template>

<style scoped>
.live {
  position: relative;
}
.viz-legend {
  margin-bottom: 6px;
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
</style>
