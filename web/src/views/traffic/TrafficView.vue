<script setup lang="ts">
import { computed, h, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NDatePicker, NEmpty, NRadioButton, NRadioGroup, NSelect, NSpin, NTag, NText,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, type AnalysisItem, type AnalysisSeries, type TrafficAnalysis } from '../../api'
import { formatBytes } from '../../composables/format'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import TrendBars from '../../components/charts/TrendBars.vue'
import type { TrendSeries } from '../../components/charts/types'
import FlowsTab from './FlowsTab.vue'
import ConnectionsTab from './ConnectionsTab.vue'
import MobileCards from '../../components/MobileCards'
import { useIsMobile } from '../../composables/viewport'

// 'connections' shows the live connection list instead of statistics.
type Kind = TrafficAnalysis['kind'] | 'flows' | 'connections'
type Dim = 'user' | 'ip' | 'site' | 'outbound'
type Gran = TrafficAnalysis['gran']
type Metric = 'total' | 'down' | 'up'
type Preset = 'today' | 'yesterday' | 'last7' | 'last30' | 'last90' | 'thisMonth' | 'lastMonth' | 'custom'
type CmpMode = 'none' | 'prev' | 'week' | 'month' | 'custom'

const { t } = useI18n()
const isMobile = useIsMobile()
const feedback = useApplyFeedback()

// --- dates (local calendar days, as YYYY-MM-DD) ---
const DAY = 86_400_000
const ymd = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
const parse = (s: string) => {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y!, m! - 1, d!)
}
const addDays = (d: Date, n: number) => new Date(d.getFullYear(), d.getMonth(), d.getDate() + n)
const addMonths = (d: Date, n: number) => {
  const r = new Date(d.getFullYear(), d.getMonth() + n, 1)
  r.setDate(Math.min(d.getDate(), new Date(r.getFullYear(), r.getMonth() + 1, 0).getDate()))
  return r
}

// Filters live in the URL so a view can be bookmarked or shared.
const route = useRoute()
const router = useRouter()
const qs = (k: string) => (typeof route.query[k] === 'string' ? (route.query[k] as string) : '')
const oneOf = <T extends string>(v: string, all: readonly T[], def: T): T => ((all as readonly string[]).includes(v) ? (v as T) : def)
const dateMs = (v: string) => (/^\d{4}-\d{2}-\d{2}$/.test(v) ? parse(v).getTime() : null)

const kind = ref<Kind>(oneOf(qs('kind'), ['user', 'inbound', 'outbound', 'flows', 'connections'] as const, 'user'))
const isFlows = computed(() => kind.value === 'flows')
const isConns = computed(() => kind.value === 'connections')
const DIMS: Dim[] = ['user', 'ip', 'site', 'outbound']
const flowDims = ref<Dim[]>(qs('cols') ? DIMS.filter((d) => qs('cols').split(',').includes(d)) : ['ip', 'site'])
if (flowDims.value.length < 2) flowDims.value = ['ip', 'site']
const preset = ref<Preset>(
  oneOf(qs('range'), ['today', 'yesterday', 'last7', 'last30', 'last90', 'thisMonth', 'lastMonth', 'custom'] as const, 'last7'),
)
const customRange = ref<[number, number] | null>(
  dateMs(qs('from')) !== null && dateMs(qs('to')) !== null ? [dateMs(qs('from'))!, dateMs(qs('to'))!] : null,
)
const granPick = ref<Gran | null>(qs('gran') ? oneOf(qs('gran'), ['hour', 'day', 'month'] as const, 'day') : null) // null = automatic
const metric = ref<Metric>(oneOf(qs('metric'), ['total', 'down', 'up'] as const, 'total'))
const cmpMode = ref<CmpMode>(oneOf(qs('cmp'), ['none', 'prev', 'week', 'month', 'custom'] as const, 'prev'))
const cmpCustom = ref<number | null>(dateMs(qs('cmp_from')))
const focus = ref<string | null>(qs('focus') || null)

const data = ref<TrafficAnalysis | null>(null)
const loading = ref(false)

const range = computed(() => {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  switch (preset.value) {
    case 'today':
      return { from: today, to: today }
    case 'yesterday':
      return { from: addDays(today, -1), to: addDays(today, -1) }
    case 'last30':
      return { from: addDays(today, -29), to: today }
    case 'last90':
      return { from: addDays(today, -89), to: today }
    case 'thisMonth':
      return { from: new Date(today.getFullYear(), today.getMonth(), 1), to: today }
    case 'lastMonth':
      return { from: new Date(today.getFullYear(), today.getMonth() - 1, 1), to: new Date(today.getFullYear(), today.getMonth(), 0) }
    case 'custom':
      if (customRange.value) return { from: new Date(customRange.value[0]), to: new Date(customRange.value[1]) }
      return { from: addDays(today, -6), to: today }
    default:
      return { from: addDays(today, -6), to: today }
  }
})
const days = computed(() => Math.round((range.value.to.getTime() - range.value.from.getTime()) / DAY) + 1)

const granAllowed = computed<Record<Gran, boolean>>(() => ({
  hour: days.value <= 14,
  day: true,
  month: days.value >= 28,
}))
const gran = computed<Gran>(() => {
  if (granPick.value && granAllowed.value[granPick.value]) return granPick.value
  return days.value <= 2 ? 'hour' : days.value <= 92 ? 'day' : 'month'
})

const cmpFrom = computed(() => {
  const f = range.value.from
  switch (cmpMode.value) {
    case 'prev':
      return addDays(f, -days.value)
    case 'week':
      return addDays(f, -7)
    case 'month':
      return addMonths(f, -1)
    case 'custom':
      return cmpCustom.value ? new Date(cmpCustom.value) : null
    default:
      return null
  }
})

async function load() {
  if (kind.value === 'flows' || kind.value === 'connections') return
  const p = new URLSearchParams({ kind: kind.value, gran: gran.value, from: ymd(range.value.from), to: ymd(range.value.to) })
  if (cmpFrom.value) p.set('cmp_from', ymd(cmpFrom.value))
  loading.value = true
  try {
    data.value = await api<TrafficAnalysis>('/stats/analysis?' + p.toString())
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  } finally {
    loading.value = false
  }
}
watch([kind, range, gran, cmpFrom], load, { immediate: true })
watch(kind, (_, old) => old && (focus.value = null))
watch([kind, preset, customRange, granPick, metric, cmpMode, cmpCustom, focus, flowDims], () => {
  if (kind.value === 'connections') return void router.replace({ query: { kind: 'connections' } })
  const q: Record<string, string> = { kind: kind.value, range: preset.value, metric: metric.value, cmp: cmpMode.value }
  if (preset.value === 'custom') Object.assign(q, { from: ymd(range.value.from), to: ymd(range.value.to) })
  if (granPick.value) q.gran = granPick.value
  if (cmpMode.value === 'custom' && cmpFrom.value) q.cmp_from = ymd(cmpFrom.value)
  if (focus.value) q.focus = focus.value
  if (kind.value === 'flows') {
    delete q.cmp
    q.cols = flowDims.value.join(',')
  }
  router.replace({ query: q })
})

// --- values ---
const pick = (p: [number, number]) => (metric.value === 'down' ? p[1] : metric.value === 'up' ? p[0] : p[0] + p[1])
const sum = (s: AnalysisSeries | undefined) =>
  !s ? 0 : metric.value === 'down' ? s.down : metric.value === 'up' ? s.up : s.up + s.down

const itemName = (it: AnalysisItem) => (it.gone && kind.value === 'user' ? t('traffic.deletedUser', { key: it.key }) : it.name)

/** Items with traffic in either period, in the server's stable order. */
const items = computed(() => (data.value?.items ?? []).filter((it) => sum(it) > 0 || sum(it.cmp) > 0))
const grand = computed(() => items.value.reduce((n, it) => n + sum(it), 0))

// Up to six series keep their own colour; beyond that the smallest fold
// into "Other". Colours follow the stable order, not the ranking.
const SLOTS = 6
const shown = computed(() => {
  const ranked = [...items.value].sort((a, b) => sum(b) - sum(a))
  const keep = ranked.length <= SLOTS ? ranked : ranked.slice(0, SLOTS - 1)
  const keys = new Set(keep.map((it) => it.key))
  const individual = items.value.filter((it) => keys.has(it.key))
  const color = new Map(individual.map((it, i) => [it.key, `var(--viz-cat-${i + 1})`]))
  const rest = items.value.filter((it) => !keys.has(it.key))
  return { individual, rest, color }
})
const colorOf = (key: string) => shown.value.color.get(key) ?? 'var(--viz-other)'

const focused = computed(() => items.value.find((it) => it.key === focus.value) ?? null)

const series = computed<TrendSeries[]>(() => {
  const n = data.value?.range.buckets.length ?? 0
  if (focused.value) {
    const it = focused.value
    if (metric.value === 'total') {
      return [
        { key: 'down', name: t('traffic.down'), color: 'var(--viz-down)', values: it.series.map((p) => p[1]) },
        { key: 'up', name: t('traffic.up'), color: 'var(--viz-up)', values: it.series.map((p) => p[0]) },
      ]
    }
    return [{ key: it.key, name: itemName(it), color: colorOf(it.key), values: it.series.map(pick) }]
  }
  const out: TrendSeries[] = shown.value.individual.map((it) => ({
    key: it.key, name: itemName(it), color: colorOf(it.key), values: it.series.map(pick),
  }))
  if (shown.value.rest.length) {
    const values = Array.from({ length: n }, (_, i) => shown.value.rest.reduce((s, it) => s + pick(it.series[i]!), 0))
    out.push({ key: '__other', name: t('traffic.other', { n: shown.value.rest.length }), color: 'var(--viz-other)', values })
  }
  return out
})

const compareSeries = computed(() => {
  const c = data.value?.cmp
  if (!c) return null
  const src = focused.value ? [focused.value] : items.value
  const values = c.buckets.map((_, i) => (cmpValid.value[i] ? src.reduce((s, it) => s + (it.cmp ? pick(it.cmp.series[i]!) : 0), 0) : null))
  return { name: t('traffic.cmpSeries'), labels: c.buckets.map(longLabel), values }
})

// --- like-for-like comparison ---
// A comparison bucket counts only if the current period has the bucket at
// the same position (today so far vs the same hours yesterday, not all of
// yesterday) and data was already being collected then.
const cmpValid = computed(() => {
  const d = data.value
  if (!d?.cmp) return []
  const n = d.range.buckets.length
  return d.cmp.buckets.map((b, i) => {
    if (i >= n || !d.first_day) return false
    if (b.length === 13) return !!d.first_hour && b >= d.first_hour
    if (b.length === 10) return b >= d.first_day
    return b >= d.first_day.slice(0, 7)
  })
})
const cmpSum = (it: AnalysisItem) =>
  it.cmp ? it.cmp.series.reduce((s, p, i) => s + (cmpValid.value[i] ? pick(p) : 0), 0) : 0
/** The current period over the same buckets the comparison counts. */
const likeSum = (it: AnalysisItem) => it.series.reduce((s, p, i) => s + (cmpValid.value[i] ? pick(p) : 0), 0)

// --- labels ---
const weekday = (d: Date) => d.toLocaleDateString(undefined, { weekday: 'short' })
function longLabel(b: string) {
  if (b.length === 13) return `${b.slice(5, 10)} ${b.slice(11)}:00`
  if (b.length === 10) return `${b} ${weekday(parse(b))}`
  return b
}
function shortLabel(b: string) {
  if (b.length === 13) return days.value > 1 && b.endsWith(' 00') ? b.slice(5, 10) : `${b.slice(11)}:00`
  if (b.length === 10) return b.slice(5)
  return b
}
const labels = computed(() => (data.value?.range.buckets ?? []).map(longLabel))
const ticks = computed(() => (data.value?.range.buckets ?? []).map(shortLabel))

// --- tiles ---
const subject = computed(() => (focused.value ? [focused.value] : items.value))
const total = computed(() => subject.value.reduce((n, it) => n + sum(it), 0))
const totalCmp = computed(() => subject.value.reduce((n, it) => n + cmpSum(it), 0))
const totalLike = computed(() => subject.value.reduce((n, it) => n + likeSum(it), 0))
const totalSplit = computed(() => ({
  down: subject.value.reduce((n, it) => n + it.down, 0),
  up: subject.value.reduce((n, it) => n + it.up, 0),
}))
const elapsedDays = computed(() => {
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const end = range.value.to > today ? today : range.value.to
  return Math.max(1, Math.round((end.getTime() - range.value.from.getTime()) / DAY) + 1)
})
const peak = computed(() => {
  const totals = labels.value.map((_, i) => series.value.reduce((s, x) => s + (x.values[i] ?? 0), 0))
  let best = -1
  totals.forEach((v, i) => {
    if (best < 0 || v > totals[best]!) best = i
  })
  return best >= 0 && totals[best]! > 0 ? { v: totals[best]!, label: labels.value[best] } : null
})
const pct = (a: number, b: number) => {
  if (!b) return null
  const v = (a - b) / b
  return `${v > 0 ? '+' : ''}${(v * 100).toFixed(Math.abs(v) < 0.1 ? 1 : 0)}%`
}

// --- notes ---
const notes = computed(() => {
  const d = data.value
  if (!d) return []
  const out: string[] = []
  if (!d.first_day) return [t('traffic.notCollected')]
  const earliest = d.cmp && d.cmp.from < d.range.from ? d.cmp.from : d.range.from
  if (earliest < d.first_day) out.push(t('traffic.noData', { day: d.first_day }))
  if (d.gran === 'hour' && d.first_hour && earliest + ' 00' < d.first_hour) out.push(t('traffic.noHourly', { hour: d.first_hour }))
  return out
})

// --- table ---
interface Row {
  key: string
  it: AnalysisItem
  name: string
  down: number
  up: number
  value: number
  cmp: number
  like: number
}
const rows = computed<Row[]>(() =>
  [...items.value]
    .map((it) => ({ key: it.key, it, name: itemName(it), down: it.down, up: it.up, value: sum(it), cmp: cmpSum(it), like: likeSum(it) }))
    .sort((a, b) => b.value - a.value || b.cmp - a.cmp),
)
const columns = computed<DataTableColumns<Row>>(() => {
  const cols: DataTableColumns<Row> = [
    {
      title: t('traffic.name'),
      key: 'name',
      minWidth: 170,
      render: (r) =>
        h('span', { class: 'name-cell' }, [
          h('i', { class: 'swatch', style: { background: colorOf(r.key) } }),
          h(NText, { strong: focus.value === r.key }, () => r.name),
          r.it.gone && kind.value !== 'user' ? h(NTag, { size: 'tiny', bordered: false }, () => t('traffic.gone')) : null,
        ]),
    },
    { title: t('traffic.down'), key: 'down', width: 110, align: 'right', sorter: (a, b) => a.down - b.down, render: (r) => formatBytes(r.down) },
    { title: t('traffic.up'), key: 'up', width: 110, align: 'right', sorter: (a, b) => a.up - b.up, render: (r) => formatBytes(r.up) },
    {
      title: t('traffic.share'),
      key: 'share',
      width: 150,
      render: (r) => {
        const share = grand.value ? r.value / grand.value : 0
        return h('div', { class: 'share' }, [
          h('div', { class: 'share-track' }, [h('div', { class: 'share-bar', style: { width: `${(share * 100).toFixed(1)}%` } })]),
          h('span', { class: 'num' }, `${(share * 100).toFixed(share < 0.1 ? 1 : 0)}%`),
        ])
      },
    },
    {
      title: metric.value === 'total' ? t('traffic.total') : metric.value === 'down' ? t('traffic.down') : t('traffic.up'),
      key: 'value',
      width: 120,
      align: 'right',
      sorter: (a, b) => a.value - b.value,
      render: (r) => h('b', { class: 'num' }, formatBytes(r.value)),
    },
  ]
  if (data.value?.cmp) {
    cols.push(
      { title: t('traffic.cmpCol'), key: 'cmp', width: 120, align: 'right', sorter: (a, b) => a.cmp - b.cmp, render: (r) => formatBytes(r.cmp) },
      {
        title: t('traffic.changeCol'),
        key: 'change',
        width: 90,
        align: 'right',
        render: (r) => h(NText, { depth: 2, class: 'num' }, () => pct(r.like, r.cmp) ?? '—'),
      },
    )
  }
  return cols
})
const rowProps = (r: Row) => ({
  style: 'cursor: pointer',
  onClick: () => (focus.value = focus.value === r.key ? null : r.key),
})

const presetOptions = computed(() =>
  (['today', 'yesterday', 'last7', 'last30', 'last90', 'thisMonth', 'lastMonth', 'custom'] as Preset[]).map((p) => ({
    label: t(`traffic.${p}`),
    value: p,
  })),
)
const cmpOptions = computed(() => [
  { label: t('traffic.cmpNone'), value: 'none' },
  { label: t('traffic.cmpPrev'), value: 'prev' },
  { label: t('traffic.cmpWeek'), value: 'week' },
  { label: t('traffic.cmpMonth'), value: 'month' },
  { label: t('traffic.cmpCustom'), value: 'custom' },
])
const isFuture = (ts: number) => ts > Date.now()
</script>

<template>
  <div class="filters">
    <NRadioGroup v-model:value="kind" size="small">
      <NRadioButton value="user">{{ t('traffic.byUser') }}</NRadioButton>
      <NRadioButton value="inbound">{{ t('traffic.byInbound') }}</NRadioButton>
      <NRadioButton value="outbound">{{ t('traffic.byOutbound') }}</NRadioButton>
      <NRadioButton value="flows">{{ t('traffic.byFlows') }}</NRadioButton>
      <NRadioButton value="connections">{{ t('traffic.byConnections') }}</NRadioButton>
    </NRadioGroup>
    <template v-if="!isConns">
    <NSelect v-model:value="preset" :options="presetOptions" size="small" class="sel" />
    <NDatePicker
      v-if="preset === 'custom'"
      v-model:value="customRange"
      type="daterange"
      size="small"
      :is-date-disabled="isFuture"
      class="picker"
    />
    <NRadioGroup v-if="!isFlows" :value="gran" size="small" @update:value="(g: Gran) => (granPick = g)">
      <NRadioButton value="hour" :disabled="!granAllowed.hour">{{ t('traffic.hour') }}</NRadioButton>
      <NRadioButton value="day">{{ t('traffic.day') }}</NRadioButton>
      <NRadioButton value="month" :disabled="!granAllowed.month">{{ t('traffic.month') }}</NRadioButton>
    </NRadioGroup>
    <NRadioGroup v-model:value="metric" size="small">
      <NRadioButton value="total">{{ t('traffic.total') }}</NRadioButton>
      <NRadioButton value="down">{{ t('traffic.down') }}</NRadioButton>
      <NRadioButton value="up">{{ t('traffic.up') }}</NRadioButton>
    </NRadioGroup>
    <NSelect v-if="!isFlows" v-model:value="cmpMode" :options="cmpOptions" size="small" class="sel wide" />
    <NDatePicker v-if="!isFlows && cmpMode === 'custom'" v-model:value="cmpCustom" type="date" size="small" :is-date-disabled="isFuture" class="picker" />
    </template>
  </div>

  <ConnectionsTab v-if="isConns" />

  <FlowsTab
    v-else-if="isFlows"
    v-model:focus="focus"
    v-model:dims="flowDims"
    :from="ymd(range.from)"
    :to="ymd(range.to)"
    :metric="metric"
  />

  <template v-else>
  <NAlert v-for="n in notes" :key="n" type="info" :show-icon="false" class="note">{{ n }}</NAlert>

  <div class="tiles">
    <NCard size="small" class="tile">
      <div class="tile-label">{{ t('traffic.totalTile') }}</div>
      <div class="hero">{{ formatBytes(total) }}</div>
      <div class="tile-sub">
        <span>{{ t('traffic.down') }} {{ formatBytes(totalSplit.down) }}</span>
        <span>{{ t('traffic.up') }} {{ formatBytes(totalSplit.up) }}</span>
      </div>
    </NCard>
    <NCard size="small" class="tile">
      <div class="tile-label">{{ data?.gran === 'hour' ? t('traffic.hourAvgTile') : t('traffic.avgTile') }}</div>
      <div class="hero">{{ formatBytes(data?.gran === 'hour' ? total / Math.max(1, labels.length) : total / elapsedDays) }}</div>
      <div class="tile-sub">{{ data?.range.from }} – {{ data?.range.to }}</div>
    </NCard>
    <NCard size="small" class="tile">
      <div class="tile-label">{{ t('traffic.peakTile') }}</div>
      <div class="hero">{{ peak ? formatBytes(peak.v) : '—' }}</div>
      <div class="tile-sub">{{ peak?.label ?? '' }}</div>
    </NCard>
    <NCard v-if="data?.cmp" size="small" class="tile">
      <div class="tile-label">{{ t('traffic.change') }}</div>
      <div class="hero">{{ pct(totalLike, totalCmp) ?? '—' }}</div>
      <div class="tile-sub">{{ t('traffic.cmpTotal', { v: formatBytes(totalCmp) }) }}</div>
    </NCard>
  </div>

  <NCard size="small" class="block">
    <template #header>
      {{ t('traffic.trend') }}
      <NButton v-if="focused" size="tiny" secondary class="clear" @click="focus = null">
        {{ t('traffic.focus', { name: itemName(focused) }) }} ✕
      </NButton>
    </template>
    <template #header-extra>
      <NText v-if="data?.cmp" depth="3" class="small">
        {{ t('traffic.cmpRange', { from: data.cmp.from, to: data.cmp.to }) }}
        <template v-if="data.cmp_partial_today"> · {{ t('traffic.cmpPartial', { hour: data.now.slice(11) }) }}</template>
      </NText>
    </template>
    <NSpin :show="loading && !!data" :delay="300">
      <div v-if="data && items.length" class="viz-legend legend">
        <span
          v-for="s in [...series].reverse()"
          :key="s.key"
          :class="{ clickable: !focused && s.key !== '__other' }"
          @click="!focused && s.key !== '__other' && (focus = s.key)"
        >
          <i class="viz-key sq" :style="{ background: s.color }" />{{ s.name }}
        </span>
        <span v-if="compareSeries"><i class="viz-key" style="background: var(--viz-cmp)" />{{ compareSeries.name }}</span>
        <NText v-if="!focused && items.length > 1" depth="3" class="hint">{{ t('traffic.focusHint') }}</NText>
      </div>
      <TrendBars
        v-if="data && items.length"
        :labels="labels"
        :ticks="ticks"
        :series="series"
        :compare="compareSeries"
        :total-label="t('traffic.total')"
        :change-label="t('traffic.change')"
      />
      <NEmpty v-else-if="data" :description="t('traffic.empty')" class="empty" />
      <div v-else class="placeholder" />
    </NSpin>
  </NCard>

  <NCard size="small" :title="t('traffic.table')" class="block">
    <MobileCards
      v-if="isMobile"
      :columns="columns"
      :data="rows"
      :row-key="(r: Row) => r.key"
      title-key="name"
      :empty="t('traffic.empty')"
      :on-row-click="(r: Row) => (focus = focus === r.key ? null : r.key)"
    />
    <NDataTable
      v-else
      :columns="columns"
      :data="rows"
      :row-key="(r: Row) => r.key"
      :row-props="rowProps"
      :bordered="false"
      size="small"
      :scroll-x="data?.cmp ? 820 : 610"
    >
      <template #empty><NEmpty :description="t('traffic.empty')" /></template>
    </NDataTable>
  </NCard>
  </template>
</template>

<style scoped>
.filters {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 12px;
  align-items: center;
  margin-bottom: 16px;
}
.sel {
  width: 130px;
}
.sel.wide {
  width: 170px;
}
.picker {
  width: 260px;
}
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
}
.tile-sub {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 12px;
  font-size: 12px;
  color: var(--viz-muted);
  margin-top: 2px;
}
.block {
  margin-bottom: 16px;
}
.clear {
  margin-left: 10px;
}
.small {
  font-size: 12px;
}
.legend {
  margin-bottom: 8px;
}
.legend .clickable {
  cursor: pointer;
}
.legend .clickable:hover {
  color: var(--viz-text);
}
.legend .hint {
  font-size: 12px;
  margin-left: auto;
}
.viz-key.sq {
  width: 10px;
  height: 10px;
  border-radius: 2px;
}
.empty {
  padding: 48px 0;
}
.placeholder {
  height: 280px;
}
:deep(.name-cell) {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
:deep(.swatch) {
  width: 10px;
  height: 10px;
  border-radius: 2px;
  flex: none;
}
:deep(.share) {
  display: flex;
  align-items: center;
  gap: 8px;
}
:deep(.share-track) {
  flex: 1;
  height: 6px;
  border-radius: 3px;
  background: var(--viz-grid);
  overflow: hidden;
}
:deep(.share-bar) {
  height: 100%;
  border-radius: 3px;
  background: var(--viz-muted);
}
:deep(.num) {
  font-variant-numeric: tabular-nums;
}
@media (max-width: 600px) {
  .hero {
    font-size: 20px;
  }
}
</style>
