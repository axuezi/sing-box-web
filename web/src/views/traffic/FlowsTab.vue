<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NCheckbox, NCheckboxGroup, NDataTable, NEmpty, NSpin, NSwitch, NText, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api } from '../../api'
import { formatBytes } from '../../composables/format'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'
import FlowChart from '../../components/charts/FlowChart.vue'
import type { FlowLink, FlowNode } from '../../components/charts/types'
import MobileCards from '../../components/MobileCards'

type Metric = 'total' | 'down' | 'up'
type Dim = 'user' | 'ip' | 'site' | 'outbound'

interface FlowRow {
  user: string
  ip: string
  site: string
  outbound: string
  up: number
  down: number
  conns: number
}
interface FlowsResponse {
  enabled: boolean
  available: boolean
  from: string
  to: string
  first_hour: string
  keep_days: number
  truncated: boolean
  rows: FlowRow[]
}

const props = defineProps<{ from: string; to: string; metric: Metric }>()
const focus = defineModel<string | null>('focus', { default: null })
const dims = defineModel<Dim[]>('dims', { default: () => ['ip', 'site'] })

const { t } = useI18n()
const isMobile = useIsMobile()
const feedback = useApplyFeedback()
const dialog = useDialog()

const data = ref<FlowsResponse | null>(null)
const loading = ref(false)
const busy = ref(false)

async function load() {
  loading.value = true
  try {
    data.value = await api<FlowsResponse>(`/stats/flows?from=${props.from}&to=${props.to}`)
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  } finally {
    loading.value = false
  }
}
watch(() => [props.from, props.to], load, { immediate: true })

async function setEnabled(on: boolean) {
  busy.value = true
  try {
    await api('/settings/flows', { method: 'PUT', body: JSON.stringify({ enabled: on }) })
    await load()
  } catch (e) {
    feedback.failure(e)
  } finally {
    busy.value = false
  }
}
function confirmEnable() {
  dialog.info({
    title: t('flows.enableTitle'),
    content: t('flows.enableBody', { days: data.value?.keep_days ?? 31 }),
    positiveText: t('flows.enable'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => setEnabled(true),
  })
}
function confirmClear() {
  dialog.warning({
    title: t('flows.clearTitle'),
    content: t('flows.clearBody'),
    positiveText: t('flows.clear'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        await api('/stats/flows', { method: 'DELETE' })
        focus.value = null
        await load()
      } catch (e) {
        feedback.failure(e)
      }
    },
  })
}

// --- values ---
const val = (r: { up: number; down: number }) => (props.metric === 'down' ? r.down : props.metric === 'up' ? r.up : r.up + r.down)
const rows = computed(() => (data.value?.rows ?? []).filter((r) => val(r) > 0))
const grand = computed(() => rows.value.reduce((n, r) => n + val(r), 0))

const ORDER: Dim[] = ['user', 'ip', 'site', 'outbound']
const LIMIT: Record<Dim, number> = { user: 8, ip: 8, site: 12, outbound: 6 }
const cols = computed(() => ORDER.filter((d) => dims.value.includes(d)))

const OTHER = '\u0000other'
function label(d: Dim, v: string) {
  if (v === OTHER) return ''
  if (d === 'user' && !v) return t('flows.unknownUser')
  if (d === 'site' && !v) return t('flows.foldedSites')
  if (d === 'outbound' && !v) return '—'
  return v
}

// Per column: the biggest values keep their own node, the rest fold into
// "Other (n)".
const kept = computed(() => {
  const out = {} as Record<Dim, { keep: Set<string>; ranked: string[]; rest: number }>
  for (const d of ORDER) {
    const tot = new Map<string, number>()
    for (const r of rows.value) tot.set(r[d], (tot.get(r[d]) ?? 0) + val(r))
    const ranked = [...tot.keys()].sort((a, b) => tot.get(b)! - tot.get(a)!)
    const n = ranked.length <= LIMIT[d] ? ranked.length : LIMIT[d] - 1
    out[d] = { keep: new Set(ranked.slice(0, n)), ranked: ranked.slice(0, n), rest: ranked.length - n }
  }
  return out
})
const fold = (d: Dim, v: string) => (kept.value[d].keep.has(v) ? v : OTHER)
const nodeId = (d: Dim, v: string) => `${d}:${v}`

// Every column colours its six biggest nodes (in a stable, sorted order);
// smaller ones and "Other" stay neutral. Each column starts where the one
// before it stopped in the palette, so neighbouring columns differ even
// when each has a single node.
const colorOf = computed(() => {
  const m = new Map<string, string>()
  let next = 0
  for (const d of cols.value) {
    const coloured = kept.value[d].ranked.slice(0, 6).slice().sort()
    coloured.forEach((v) => m.set(nodeId(d, v), `var(--viz-cat-${(next++ % 6) + 1})`))
  }
  return m
})
const nodeColor = (id: string) => colorOf.value.get(id) ?? 'var(--viz-other)'

const focusParts = computed(() => {
  const f = focus.value
  if (!f) return null
  const i = f.indexOf(':')
  const d = f.slice(0, i) as Dim
  return cols.value.includes(d) ? { d, v: f.slice(i + 1) } : null
})
const onPath = (r: FlowRow) => !focusParts.value || fold(focusParts.value.d, r[focusParts.value.d]) === focusParts.value.v

const flow = computed(() => {
  const cs = cols.value
  const nodes: FlowNode[] = []
  const links: FlowLink[] = []
  if (cs.length < 2) return { nodes, links }
  const lit = new Set<string>()
  const linkMap = new Map<string, FlowLink>()
  for (const r of rows.value) {
    const v = val(r)
    const path = cs.map((d) => nodeId(d, fold(d, r[d])))
    const on = !!focusParts.value && onPath(r)
    if (on) path.forEach((id) => lit.add(id))
    for (let i = 0; i + 1 < path.length; i++) {
      // A link fades from its source's colour to its target's.
      const k = `${path[i]}>${path[i + 1]}`
      let l = linkMap.get(k)
      if (!l) {
        l = { source: path[i]!, target: path[i + 1]!, value: 0, color: nodeColor(path[i]!), to: nodeColor(path[i + 1]!) }
        linkMap.set(k, l)
      }
      l.value += v
      if (on) l.lit = true
    }
  }
  cs.forEach((d, ci) => {
    const { ranked, rest } = kept.value[d]
    for (const v of ranked) {
      nodes.push({
        id: nodeId(d, v), name: label(d, v), column: ci, selectable: true, lit: lit.has(nodeId(d, v)),
        color: nodeColor(nodeId(d, v)),
      })
    }
    if (rest) {
      nodes.push({
        id: nodeId(d, OTHER), name: t('flows.other', { n: rest }), column: ci, selectable: true,
        lit: lit.has(nodeId(d, OTHER)), color: 'var(--viz-other)',
      })
    }
  })
  links.push(...linkMap.values())
  return { nodes, links }
})

// --- tiles ---
const distinct = (d: Dim) => new Set(rows.value.map((r) => r[d])).size
const tiles = computed(() => ({
  total: grand.value,
  ips: distinct('ip'),
  sites: distinct('site'),
  conns: rows.value.reduce((n, r) => n + r.conns, 0),
}))

// --- notes ---
const notes = computed(() => {
  const d = data.value
  if (!d || !d.enabled && !rows.value.length) return []
  const out: string[] = []
  if (!d.first_hour) out.push(t('flows.noneYet'))
  else if (d.from + ' 00' < d.first_hour) out.push(t('flows.since', { hour: d.first_hour, days: d.keep_days }))
  if (d.truncated) out.push(t('flows.truncated'))
  const noDomain = rows.value.reduce((n, r) => n + (/^[\d.:a-f]+$/i.test(r.site) && /[.:]/.test(r.site) ? val(r) : 0), 0)
  if (grand.value && noDomain / grand.value > 0.2) out.push(t('flows.noDomain', { pct: Math.round((noDomain / grand.value) * 100) }))
  if (dims.value.includes('user')) {
    const unknown = rows.value.reduce((n, r) => n + (r.user ? 0 : val(r)), 0)
    if (grand.value && unknown / grand.value > 0.05) out.push(t('flows.unknownShare', { pct: Math.round((unknown / grand.value) * 100) }))
  }
  return out
})

// --- table ---
interface Row extends FlowRow {
  key: string
  value: number
}
const tableRows = computed<Row[]>(() =>
  rows.value
    .filter(onPath)
    .map((r) => ({ ...r, key: `${r.user}|${r.ip}|${r.site}|${r.outbound}`, value: val(r) }))
    .sort((a, b) => b.value - a.value),
)
const columns = computed<DataTableColumns<Row>>(() => [
  { title: t('flows.ip'), key: 'ip', minWidth: 130, render: (r) => h('span', { class: 'mono' }, r.ip) },
  { title: t('flows.user'), key: 'user', minWidth: 90, render: (r) => (r.user ? r.user : h(NText, { depth: 3 }, () => t('flows.unknownUser'))) },
  { title: t('flows.site'), key: 'site', minWidth: 160, render: (r) => label('site', r.site) },
  { title: t('flows.outbound'), key: 'outbound', minWidth: 90, render: (r) => label('outbound', r.outbound) },
  { title: t('traffic.down'), key: 'down', width: 110, align: 'right', sorter: (a, b) => a.down - b.down, render: (r) => formatBytes(r.down) },
  { title: t('traffic.up'), key: 'up', width: 110, align: 'right', sorter: (a, b) => a.up - b.up, render: (r) => formatBytes(r.up) },
  {
    title: props.metric === 'total' ? t('traffic.total') : props.metric === 'down' ? t('traffic.down') : t('traffic.up'),
    key: 'value', width: 110, align: 'right', sorter: (a, b) => a.value - b.value,
    render: (r) => h('b', { class: 'num' }, formatBytes(r.value)),
  },
  { title: t('flows.conns'), key: 'conns', width: 80, align: 'right', sorter: (a, b) => a.conns - b.conns },
])
const page = ref(1)
watch([tableRows], () => (page.value = 1))

const dimOptions = computed(() => ORDER.map((d) => ({ value: d, label: t(`flows.dim.${d}`) })))
function setDims(v: (string | number)[]) {
  if (v.length >= 2) dims.value = ORDER.filter((d) => v.includes(d))
}
const selectNode = (id: string) => (focus.value = focus.value === id ? null : id)
const focusName = computed(() => {
  const f = focusParts.value
  if (!f) return ''
  return f.v === OTHER ? t('flows.otherOf', { dim: t(`flows.dim.${f.d}`) }) : label(f.d, f.v)
})
</script>

<template>
  <NSpin :show="loading && !data" :delay="300">
    <div v-if="data && !data.enabled && !rows.length" class="off">
      <NCard size="small">
        <NEmpty :description="t('flows.offTitle')">
          <template #extra>
            <p class="off-text">{{ t('flows.offBody', { days: data.keep_days }) }}</p>
            <NButton type="primary" :loading="busy" @click="confirmEnable">{{ t('flows.enable') }}</NButton>
          </template>
        </NEmpty>
      </NCard>
    </div>

    <template v-else-if="data">
      <NAlert v-for="n in notes" :key="n" type="info" :show-icon="false" class="note">{{ n }}</NAlert>

      <div class="tiles">
        <NCard size="small">
          <div class="tile-label">{{ t('flows.totalTile') }}</div>
          <div class="hero">{{ formatBytes(tiles.total) }}</div>
        </NCard>
        <NCard size="small">
          <div class="tile-label">{{ t('flows.ipsTile') }}</div>
          <div class="hero">{{ tiles.ips }}</div>
        </NCard>
        <NCard size="small">
          <div class="tile-label">{{ t('flows.sitesTile') }}</div>
          <div class="hero">{{ tiles.sites }}</div>
        </NCard>
        <NCard size="small">
          <div class="tile-label">{{ t('flows.connsTile') }}</div>
          <div class="hero">{{ tiles.conns }}</div>
        </NCard>
      </div>

      <NCard size="small" class="block">
        <template #header>
          {{ t('flows.title') }}
          <NButton v-if="focusParts" size="tiny" secondary class="clear" @click="focus = null">
            {{ t('traffic.focus', { name: focusName }) }} ✕
          </NButton>
        </template>
        <template #header-extra>
          <div class="ctl">
            <NSwitch :value="data.enabled" :loading="busy" size="small" @update:value="(v: boolean) => (v ? confirmEnable() : setEnabled(false))" />
            <NText depth="3" class="small">{{ data.enabled ? t('flows.recording') : t('flows.paused') }}</NText>
            <NButton size="tiny" quaternary @click="confirmClear">{{ t('flows.clear') }}</NButton>
          </div>
        </template>
        <div class="dims">
          <NText depth="3" class="small">{{ t('flows.columns') }}</NText>
          <NCheckboxGroup :value="dims" @update:value="setDims">
            <NCheckbox v-for="o in dimOptions" :key="o.value" :value="o.value" :label="o.label" :disabled="dims.length <= 2 && dims.includes(o.value)" />
          </NCheckboxGroup>
          <NText depth="3" class="small hint">{{ isMobile && dims.length > 2 ? t('flows.hintTouch') : t('flows.hint') }}</NText>
        </div>
        <FlowChart
          v-if="flow.links.length"
          :nodes="flow.nodes"
          :links="flow.links"
          :focus="focus"
          :label="t('flows.title')"
          @select="selectNode"
        />
        <NEmpty v-else :description="t('traffic.empty')" class="empty" />
      </NCard>

      <NCard size="small" :title="t('flows.table')" class="block">
        <MobileCards
          v-if="isMobile"
          :columns="columns"
          :data="tableRows.slice(0, page * 20)"
          :row-key="(r: Row) => r.key"
          title-key="site"
          :empty="t('traffic.empty')"
        />
        <NButton v-if="isMobile && tableRows.length > page * 20" block secondary class="more" @click="page++">
          {{ t('common.loadMore') }}
        </NButton>
        <NDataTable
          v-if="!isMobile"
          :columns="columns"
          :data="tableRows"
          :row-key="(r: Row) => r.key"
          :pagination="{ pageSize: 20 }"
          :bordered="false"
          size="small"
          :scroll-x="880"
        >
          <template #empty><NEmpty :description="t('traffic.empty')" /></template>
        </NDataTable>
      </NCard>
    </template>
    <div v-else class="placeholder" />
  </NSpin>
</template>

<style scoped>
.note {
  margin-bottom: 12px;
}
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.tile-label {
  font-size: 12px;
  color: var(--viz-text-2);
}
.hero {
  font-size: 26px;
  font-weight: 600;
  line-height: 1.3;
  color: var(--viz-text);
  font-variant-numeric: tabular-nums;
}
.block {
  margin-bottom: 16px;
}
.clear {
  margin-left: 10px;
}
.ctl {
  display: flex;
  align-items: center;
  gap: 8px;
}
.small {
  font-size: 12px;
}
.dims {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 12px;
  margin-bottom: 12px;
}
.dims .hint {
  margin-left: auto;
}
.empty {
  padding: 48px 0;
}
.placeholder {
  height: 280px;
}
.off-text {
  max-width: 520px;
  margin: 0 auto 12px;
  color: var(--viz-text-2);
  font-size: 13px;
  line-height: 1.6;
}
.more {
  margin-top: 8px;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}
:deep(.num) {
  font-variant-numeric: tabular-nums;
}
@media (max-width: 600px) {
  .hero {
    font-size: 20px;
  }
  .dims .hint {
    margin-left: 0;
  }
}
</style>
