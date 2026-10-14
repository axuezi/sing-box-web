<script setup lang="ts">
import { computed, ref } from 'vue'
import { formatBytes, niceBytesScale } from '../../composables/format'
import { useWidth } from '../../composables/size'

interface Day {
  day: string // YYYY-MM-DD
  up: number
  down: number
}
const props = withDefaults(defineProps<{ days: Day[]; labels: { down: string; up: string; total: string }; height?: number }>(), {
  height: 180,
})

const host = ref<HTMLElement>()
const width = useWidth(host)
const M = { l: 56, r: 8, t: 8, b: 22 }
const GAP = 2 // surface gap between stacked segments
const R = 4 // rounded data-end

const scale = computed(() => niceBytesScale(Math.max(1, ...props.days.map((d) => d.up + d.down))))
const iw = computed(() => Math.max(0, width.value - M.l - M.r))
const ih = computed(() => props.height - M.t - M.b)
const slot = computed(() => iw.value / Math.max(1, props.days.length))
const barW = computed(() => Math.max(2, Math.min(18, slot.value * 0.6)))
const y = (v: number) => M.t + ih.value - (v / scale.value.top) * ih.value
const yTicks = computed(() => {
  const out: number[] = []
  for (let v = 0; v <= scale.value.top + 1e-9; v += scale.value.step) out.push(v)
  return out
})
const labelEvery = computed(() => Math.max(1, Math.ceil(44 / slot.value)))
const short = (d: string) => `${Number(d.slice(5, 7))}/${Number(d.slice(8))}`

/** Bar segment rect path with the top corners rounded only on the top segment. */
function seg(x0: number, w: number, yTop: number, yBot: number, roundTop: boolean) {
  const h = yBot - yTop
  if (h <= 0) return ''
  const r = roundTop ? Math.min(R, w / 2, h) : 0
  return `M${x0},${yBot}V${yTop + r}${r ? `Q${x0},${yTop} ${x0 + r},${yTop}` : ''}H${x0 + w - r}${r ? `Q${x0 + w},${yTop} ${x0 + w},${yTop + r}` : ''}V${yBot}Z`
}

const bars = computed(() =>
  props.days.map((d, i) => {
    const cx = M.l + slot.value * (i + 0.5)
    const x0 = cx - barW.value / 2
    const base = y(0)
    const yDown = y(d.down)
    const yTotal = y(d.down + d.up)
    const hasUp = d.up > 0 && base - yTotal - (base - yDown) > GAP
    return {
      d,
      i,
      cx,
      down: seg(x0, barW.value, yDown, base, !hasUp),
      up: hasUp ? seg(x0, barW.value, yTotal, yDown - GAP, true) : '',
    }
  }),
)

const hover = ref<number | null>(null)
const hovered = computed(() => (hover.value === null ? null : props.days[hover.value]))
const tipLeft = computed(() => {
  if (hover.value === null) return 0
  const cx = M.l + slot.value * (hover.value + 0.5)
  return cx > width.value - 190 ? cx - 180 : cx + 12
})
</script>

<template>
  <div ref="host" class="bars">
    <div class="viz-legend">
      <span><i class="viz-key" style="background: var(--viz-down)" />{{ labels.down }}</span>
      <span><i class="viz-key" style="background: var(--viz-up)" />{{ labels.up }}</span>
    </div>
    <svg v-if="width" :width="width" :height="height" role="img" :aria-label="`${labels.down} / ${labels.up}`">
      <line v-for="v in yTicks" :key="'g' + v" :x1="M.l" :x2="width - M.r" :y1="y(v)" :y2="y(v)"
        :stroke="v === 0 ? 'var(--viz-axis)' : 'var(--viz-grid)'" stroke-width="1" />
      <text v-for="v in yTicks" :key="'y' + v" :x="M.l - 8" :y="y(v) + 4" text-anchor="end" class="tick">{{ formatBytes(v) }}</text>
      <g v-for="b in bars" :key="b.d.day" :opacity="hover === null || hover === b.i ? 1 : 0.55">
        <path :d="b.down" fill="var(--viz-down)" />
        <path v-if="b.up" :d="b.up" fill="var(--viz-up)" />
        <text v-if="(days.length - 1 - b.i) % labelEvery === 0" :x="b.cx" :y="height - 6" text-anchor="middle" class="tick">
          {{ short(b.d.day) }}
        </text>
        <rect :x="b.cx - slot / 2" :y="M.t" :width="slot" :height="ih" fill="transparent"
          @pointerenter="hover = b.i" @pointerleave="hover = null" />
      </g>
    </svg>
    <div v-if="hovered" class="viz-tooltip" :style="{ left: tipLeft + 'px', top: '28px' }">
      <div class="tt-title">{{ hovered.day }}</div>
      <div class="tt-row"><i class="viz-key" style="background: var(--viz-down)" />{{ labels.down }}<b>{{ formatBytes(hovered.down) }}</b></div>
      <div class="tt-row"><i class="viz-key" style="background: var(--viz-up)" />{{ labels.up }}<b>{{ formatBytes(hovered.up) }}</b></div>
      <div class="tt-row">{{ labels.total }}<b>{{ formatBytes(hovered.down + hovered.up) }}</b></div>
    </div>
  </div>
</template>

<style scoped>
.bars {
  position: relative;
}
.viz-legend {
  margin-bottom: 6px;
}
svg {
  display: block;
}
.tick {
  fill: var(--viz-muted);
  font-size: 11px;
  font-variant-numeric: tabular-nums;
}
</style>
