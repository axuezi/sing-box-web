<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { formatBytes } from '../../composables/format'
import { useWidth } from '../../composables/size'
import type { FlowLink, FlowNode } from './types'

// A Sankey-style flow chart: nodes sit in columns, links carry a value from
// a node to one in the next column and their thickness is that value.
const props = withDefaults(
  defineProps<{
    nodes: FlowNode[] // column order, then top-to-bottom order within a column
    links: FlowLink[]
    // node id that is selected: links and nodes flagged lit stay bright,
    // the rest fade (the caller knows which whole paths pass through it)
    focus?: string | null
    label: string // aria label
    height?: number
  }>(),
  { focus: null, height: 0 },
)
const emit = defineEmits<{ select: [id: string] }>()

const host = ref<HTMLElement>()
const uid = `flow${Math.random().toString(36).slice(2, 8)}`
const width = useWidth(host)

const NODE_W = 10
const PAD = 10 // between nodes of a column
const narrow = computed(() => width.value < 520)
const M = computed(() => ({ t: 6, b: 6, l: narrow.value ? 108 : 130, r: narrow.value ? 136 : 200 }))
// Minimum distance between neighbouring columns once a middle column is
// drawn: its labels sit in that gap, so the gap must hold a label.
const LANE = computed(() => (narrow.value ? 150 : 170))
const MID_W = computed(() => LANE.value - 16) // label room right of a middle column's nodes

const columns = computed(() => {
  const n = Math.max(0, ...props.nodes.map((x) => x.column)) + 1
  return Array.from({ length: n }, (_, c) => props.nodes.filter((x) => x.column === c))
})
// Drawing width: the host's width, or wider (scrolled sideways) when the
// columns would not leave their labels room.
const W = computed(() => {
  const n = columns.value.length
  const need = n > 2 ? M.value.l + (n - 1) * LANE.value + NODE_W + M.value.r : 0
  return Math.max(width.value, need)
})
const value = computed(() => {
  const inOut = new Map<string, [number, number]>()
  for (const l of props.links) {
    const s = inOut.get(l.source) ?? [0, 0]
    const t = inOut.get(l.target) ?? [0, 0]
    inOut.set(l.source, [s[0], s[1] + l.value])
    inOut.set(l.target, [t[0] + l.value, t[1]])
  }
  return (id: string) => Math.max(...(inOut.get(id) ?? [0, 0]))
})
// Labels are one line (name, value) in the last column on a wide screen and
// two lines elsewhere; leave room for every label of the busiest column.
const labelGap = (ci: number) => (ci === columns.value.length - 1 && !narrow.value ? 18 : 32)
const H = computed(
  () => props.height || Math.max(200, ...columns.value.map((c, ci) => c.length * Math.max(labelGap(ci) + 2, 24) + 12)),
)

const layout = computed(() => {
  const m = M.value
  const ih = H.value - m.t - m.b
  const cols = columns.value
  // One bytes-to-pixels scale for every column, so a node is as tall as the
  // links entering or leaving it.
  const k = Math.min(
    ...cols.map((c) => {
      const sum = c.reduce((s, x) => s + value.value(x.id), 0)
      return sum ? (ih - PAD * (c.length - 1)) / sum : Infinity
    }),
  )
  const scale = Number.isFinite(k) ? k : 0
  const span = W.value - m.l - m.r - NODE_W
  const pos = new Map<string, { x: number; y: number; h: number; node: FlowNode }>()
  cols.forEach((c, ci) => {
    const x = m.l + (cols.length > 1 ? (span * ci) / (cols.length - 1) : 0)
    const used = c.reduce((s, x) => s + Math.max(1, value.value(x.id) * scale), 0) + PAD * (c.length - 1)
    let y = m.t + (ih - used) / 2
    for (const node of c) {
      const h = Math.max(1, value.value(node.id) * scale)
      pos.set(node.id, { x, y, h, node })
      y += h + PAD
    }
  })
  // Links leave a source in target order and enter a target in source order.
  const outY = new Map<string, number>()
  const inY = new Map<string, number>()
  const order = new Map(props.nodes.map((x, i) => [x.id, i]))
  const byTarget = [...props.links].sort((a, b) => order.get(a.target)! - order.get(b.target)!)
  const start = new Map<FlowLink, number>()
  for (const l of byTarget) {
    const s = pos.get(l.source)
    if (!s) continue
    const y = outY.get(l.source) ?? s.y
    start.set(l, y)
    outY.set(l.source, y + l.value * scale)
  }
  const bySource = [...props.links].sort((a, b) => order.get(a.source)! - order.get(b.source)!)
  const links = bySource.flatMap((l) => {
    const s = pos.get(l.source)
    const t = pos.get(l.target)
    const y0 = start.get(l)
    if (!s || !t || y0 === undefined) return []
    const y1 = inY.get(l.target) ?? t.y
    inY.set(l.target, y1 + l.value * scale)
    const w = l.value * scale
    if (w < 0.5) return []
    const x0 = s.x + NODE_W
    const x1 = t.x
    const xm = (x0 + x1) / 2
    const d = `M${x0},${y0}C${xm},${y0} ${xm},${y1} ${x1},${y1}V${y1 + w}C${xm},${y1 + w} ${xm},${y0 + w} ${x0},${y0 + w}Z`
    return [{ link: l, d, x0, x1 }]
  })
  // Labels start at their node's middle and are pushed apart so that thin
  // neighbouring nodes do not print on top of each other.
  const labelY = new Map<string, number>()
  cols.forEach((c, ci) => {
    const gap = labelGap(ci)
    const ys = c.map((x) => pos.get(x.id)!.y + pos.get(x.id)!.h / 2)
    for (let i = 1; i < ys.length; i++) ys[i] = Math.max(ys[i]!, ys[i - 1]! + gap)
    const bottom = H.value - m.b - gap / 2
    if (ys.length) ys[ys.length - 1] = Math.min(ys[ys.length - 1]!, bottom)
    for (let i = ys.length - 2; i >= 0; i--) ys[i] = Math.min(ys[i]!, ys[i + 1]! - gap)
    const top = m.t + gap / 2
    if (ys.length && ys[0]! < top) for (let i = 0; i < ys.length; i++) ys[i] = Math.max(ys[i]!, i ? ys[i - 1]! + gap : top)
    c.forEach((x, i) => labelY.set(x.id, ys[i]!))
  })
  return { nodes: [...pos.values()].map((p) => ({ ...p, ly: labelY.get(p.node.id)! })), links }
})

const lastCol = computed(() => columns.value.length - 1)
const total = computed(() => columns.value[0]?.reduce((s, x) => s + value.value(x.id), 0) ?? 0)
const share = (v: number) => (total.value ? `${((v / total.value) * 100).toFixed(v / total.value < 0.1 ? 1 : 0)}%` : '')

const touches = (l: FlowLink, id: string) => l.source === id || l.target === id
type Target = { kind: 'node'; id: string } | { kind: 'link'; link: FlowLink }
// A mouse hovers; a finger has no hover, so a tap pins the tooltip instead
// (the tap also selects, which does the highlighting).
const hover = ref<Target | null>(null)
const pinned = ref<Target | null>(null)
let touch = false
function enter(e: PointerEvent, t: Target) {
  if (e.pointerType !== 'touch') hover.value = t
}
const same = (a: Target | null, b: Target) =>
  !!a && a.kind === b.kind && (a.kind === 'node' ? a.id === (b as typeof a).id : a.link === (b as typeof a).link)
function tap(t: Target, id: string) {
  if (touch) pinned.value = same(pinned.value, t) ? null : t
  pick(id)
}
function linkOpacity(l: FlowLink) {
  const h = hover.value
  if (h?.kind === 'link') return h.link === l ? 0.8 : 0.15
  if (h?.kind === 'node') return touches(l, h.id) ? 0.7 : 0.12
  if (props.focus) return l.lit ? 0.7 : 0.1
  return 0.55
}
function nodeDim(node: FlowNode) {
  const h = hover.value
  if (h?.kind === 'node') return h.id !== node.id && !props.links.some((l) => touches(l, h.id) && touches(l, node.id))
  return !!props.focus && !node.lit
}

const tipPos = ref({ x: 0, y: 0 })
function onMove(e: PointerEvent) {
  const r = host.value!.getBoundingClientRect()
  const x = e.clientX - r.left
  const flip = x > width.value - 240
  tipPos.value = { x: flip ? Math.max(0, x - 236) : x + 14, y: e.clientY - r.top + (touch ? 18 : 14) }
}
function onDown(e: PointerEvent) {
  touch = e.pointerType === 'touch'
  onMove(e)
}

// Sideways scrolling: fade whichever edge has more chart beyond it.
const scroller = ref<HTMLElement>()
const edge = ref({ left: false, right: false })
function onScroll() {
  const el = scroller.value
  if (!el) return
  edge.value = { left: el.scrollLeft > 2, right: el.scrollLeft + el.clientWidth < el.scrollWidth - 2 }
  pinned.value = null
}
watch([W, width], () => nextTick(onScroll))
const name = (id: string) => props.nodes.find((x) => x.id === id)?.name ?? id
const tip = computed(() => {
  const h = hover.value ?? pinned.value
  if (!h) return null
  if (h.kind === 'link') return { title: `${name(h.link.source)} → ${name(h.link.target)}`, v: h.link.value }
  return { title: name(h.id), v: value.value(h.id) }
})
// Fit a label into px of room; CJK glyphs are about twice as wide.
const clip = (s: string, px: number) => {
  let w = 0
  for (let i = 0; i < s.length; i++) {
    w += s.charCodeAt(i) > 0x2e80 ? 12 : 6.8
    if (w > px) return s.slice(0, Math.max(1, i - 1)) + '…'
  }
  return s
}
const room = (col: number) => (col === 0 ? M.value.l - 12 : col === lastCol.value ? M.value.r - 12 : MID_W.value - 8)
const pick = (id: string) => props.nodes.find((x) => x.id === id)?.selectable && emit('select', id)
</script>

<template>
  <div ref="host" class="flow" @pointermove="onMove" @pointerdown="onDown" @pointerleave="hover = null">
    <div ref="scroller" class="scroller" @scroll.passive="onScroll">
      <svg v-if="width" :width="W" :height="H" role="img" :aria-label="label" @click="pinned = null">
        <defs>
          <template v-for="(l, i) in layout.links" :key="'g' + i">
            <linearGradient v-if="l.link.to" :id="`${uid}-${i}`" gradientUnits="userSpaceOnUse" :x1="l.x0" :x2="l.x1" y1="0" y2="0">
              <stop offset="0" :stop-color="l.link.color" />
              <stop offset="1" :stop-color="l.link.to" />
            </linearGradient>
          </template>
        </defs>
        <path
          v-for="(l, i) in layout.links"
          :key="'l' + i"
          :d="l.d"
          :fill="l.link.to ? `url(#${uid}-${i})` : l.link.color"
          :fill-opacity="linkOpacity(l.link)"
          class="link"
          :class="{ clickable: nodes.find((x) => x.id === l.link.target)?.selectable }"
          @pointerenter="enter($event, { kind: 'link', link: l.link })"
          @pointerleave="hover = null"
          @click.stop="tap({ kind: 'link', link: l.link }, l.link.target)"
        />
        <g
          v-for="p in layout.nodes"
          :key="p.node.id"
          :class="{ clickable: p.node.selectable, dim: nodeDim(p.node) }"
          class="node"
          @pointerenter="enter($event, { kind: 'node', id: p.node.id })"
          @pointerleave="hover = null"
          @click.stop="tap({ kind: 'node', id: p.node.id }, p.node.id)"
        >
          <!-- generous hit target around the thin bar and its label -->
          <rect
            :x="p.node.column === 0 ? p.x - M.l : p.x"
            :y="Math.min(p.y, p.ly - 14) - PAD / 2"
            :width="p.node.column === 0 ? M.l + NODE_W : p.node.column === lastCol ? W - p.x : MID_W"
            :height="Math.max(p.y + p.h, p.ly + 16) - Math.min(p.y, p.ly - 14) + PAD"
            fill="transparent"
          />
          <rect :x="p.x" :y="p.y" :width="NODE_W" :height="p.h" :rx="Math.min(2, p.h / 2)" :fill="p.node.color" />
          <template v-if="p.node.column === lastCol">
            <text :x="p.x + NODE_W + 8" :y="p.ly + (narrow ? -3 : 4)" class="lbl" :class="{ strong: focus === p.node.id }">
              {{ clip(p.node.name, narrow ? room(lastCol) : room(lastCol) - 72) }}
            </text>
            <text
              :x="narrow ? p.x + NODE_W + 8 : W - 4"
              :y="p.ly + (narrow ? 12 : 4)"
              :text-anchor="narrow ? 'start' : 'end'"
              class="val"
            >
              {{ formatBytes(value(p.node.id)) }}
            </text>
          </template>
          <template v-else-if="p.node.column === 0">
            <text :x="p.x - 8" :y="p.ly - 3" text-anchor="end" class="lbl" :class="{ strong: focus === p.node.id }">
              {{ clip(p.node.name, room(0)) }}
            </text>
            <text :x="p.x - 8" :y="p.ly + 12" text-anchor="end" class="val">{{ formatBytes(value(p.node.id)) }}</text>
          </template>
          <template v-else>
            <!-- middle column: over the links, so the text gets a surface halo -->
            <text :x="p.x + NODE_W + 6" :y="p.ly - 3" class="lbl halo" :class="{ strong: focus === p.node.id }">
              {{ clip(p.node.name, room(p.node.column)) }}
            </text>
            <text :x="p.x + NODE_W + 6" :y="p.ly + 12" class="val halo">{{ formatBytes(value(p.node.id)) }}</text>
          </template>
        </g>
      </svg>
    </div>
    <div class="fade left" :class="{ on: edge.left }" />
    <div class="fade right" :class="{ on: edge.right }" />
    <div v-if="tip" class="viz-tooltip" :style="{ left: tipPos.x + 'px', top: tipPos.y + 'px' }">
      <div class="tt-title">{{ tip.title }}</div>
      <div class="tt-row">{{ formatBytes(tip.v) }}<b>{{ share(tip.v) }}</b></div>
    </div>
  </div>
</template>

<style scoped>
.flow {
  position: relative;
}
.scroller {
  overflow-x: auto;
  overflow-y: hidden;
  overscroll-behavior-x: contain;
  -webkit-overflow-scrolling: touch;
  scrollbar-width: thin;
}
svg {
  display: block;
  -webkit-tap-highlight-color: transparent;
}
/* edge fades hint that the chart continues sideways */
.fade {
  position: absolute;
  top: 0;
  bottom: 0;
  width: 28px;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.15s;
}
.fade.on {
  opacity: 1;
}
.fade.left {
  left: 0;
  background: linear-gradient(to right, var(--viz-surface), transparent);
}
.fade.right {
  right: 0;
  background: linear-gradient(to left, var(--viz-surface), transparent);
}
.link {
  transition: fill-opacity 0.12s;
}
.clickable {
  cursor: pointer;
}
.node {
  transition: opacity 0.12s;
}
.node.dim {
  opacity: 0.35;
}
.lbl {
  fill: var(--viz-text);
  font-size: 12px;
}
.lbl.strong {
  font-weight: 600;
}
.halo {
  paint-order: stroke;
  stroke: var(--viz-surface);
  stroke-width: 4px;
  stroke-linejoin: round;
}
.val {
  fill: var(--viz-text-2);
  font-size: 12px;
  font-variant-numeric: tabular-nums;
}
</style>
