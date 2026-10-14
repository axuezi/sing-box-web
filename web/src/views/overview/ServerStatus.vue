<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NProgress, NSkeleton, NText } from 'naive-ui'
import { ChevronDown } from 'lucide-vue-next'
import { api } from '../../api'
import { formatBytes } from '../../composables/format'
import { useIsMobile } from '../../composables/viewport'
import InfoRow from '../../components/InfoRow.vue'

// On a phone the host facts fold away under a toggle so the meters and the
// live figures below stay on the first screen; the choice is remembered.
const isMobile = useIsMobile()
const FACTS_KEY = 'sbw.system.facts'
const factsOpen = ref(false)
try {
  factsOpen.value = localStorage.getItem(FACTS_KEY) === '1'
} catch {}
function toggleFacts() {
  factsOpen.value = !factsOpen.value
  try {
    localStorage.setItem(FACTS_KEY, factsOpen.value ? '1' : '0')
  } catch {}
}

interface SystemInfo {
  hostname: string
  os: string
  kernel: string
  arch: string
  uptime: number
  cpu: { cores: number; model: string; busy: number; user: number; system: number; iowait: number; steal: number; window: number }
  load: [number, number, number]
  memory: { total: number; available: number; cached: number }
  swap: { total: number; used: number; in_per_sec: number; out_per_sec: number }
  disk?: { path: string; total: number; free: number; inodes: number; inodes_free: number }
}

const { t } = useI18n()
const info = ref<SystemInfo | null>(null)
const failed = ref(false)

async function load() {
  try {
    info.value = await api<SystemInfo>('/system')
    failed.value = false
  } catch {
    failed.value = true
  }
}
let timer = 0
onMounted(() => {
  load()
  timer = window.setInterval(load, 10_000)
})
onBeforeUnmount(() => clearInterval(timer))

type Level = 'ok' | 'warn' | 'crit'
const level = (v: number, warn: number, crit: number): Level => (v >= crit ? 'crit' : v >= warn ? 'warn' : 'ok')
const pct = (v: number) => `${(v * 100).toFixed(v > 0 && v < 0.1 ? 1 : 0)}%`

const m = computed(() => {
  const s = info.value
  if (!s) return null
  const memUsed = s.memory.total ? (s.memory.total - s.memory.available) / s.memory.total : 0
  const swapUsed = s.swap.total ? s.swap.used / s.swap.total : 0
  const diskUsed = s.disk && s.disk.total ? (s.disk.total - s.disk.free) / s.disk.total : 0
  const inodeUsed = s.disk && s.disk.inodes ? (s.disk.inodes - s.disk.inodes_free) / s.disk.inodes : 0
  const swapIO = s.swap.in_per_sec + s.swap.out_per_sec
  // Pushing pages out, kept up over a real interval, is memory pressure;
  // reading old pages back in, or a blip in a sub-second sample, is not.
  const swapping = s.cpu.window >= 5 && s.swap.out_per_sec >= 50
  const cores = Math.max(1, s.cpu.cores)
  return {
    cpu: { v: s.cpu.busy, lv: level(s.cpu.busy, 0.8, 0.95) },
    mem: { v: memUsed, lv: level(memUsed, 0.85, 0.95) },
    swap: { v: swapUsed, lv: swapping ? 'warn' : ('ok' as Level), io: swapIO, out: s.swap.out_per_sec },
    disk: { v: diskUsed, lv: level(diskUsed, 0.85, 0.95) },
    inode: { v: inodeUsed, lv: level(inodeUsed, 0.85, 0.95) },
    load: { lv: level(s.load[0] / cores, 1, 2) },
    iowait: { lv: level(s.cpu.iowait, 0.2, 0.5) },
    steal: { lv: level(s.cpu.steal, 0.1, 0.3) },
  }
})

// Problems worth a sentence, worst first.
const issues = computed(() => {
  const s = info.value
  const x = m.value
  if (!s || !x) return []
  const out: { lv: Level; text: string }[] = []
  const add = (lv: Level, text: string) => lv !== 'ok' && out.push({ lv, text })
  add(x.cpu.lv, t('system.issue.cpu', { v: pct(x.cpu.v) }))
  add(x.load.lv, t('system.issue.load', { v: s.load[0].toFixed(2), n: s.cpu.cores }))
  add(x.mem.lv, t('system.issue.mem', { v: formatBytes(s.memory.available) }))
  add(x.swap.lv, t('system.issue.swap', { v: Math.round(x.swap.out) }))
  add(x.disk.lv, t('system.issue.disk', { v: formatBytes(s.disk?.free ?? 0) }))
  add(x.inode.lv, t('system.issue.inode', { v: pct(x.inode.v) }))
  add(x.iowait.lv, t('system.issue.iowait', { v: pct(s.cpu.iowait) }))
  add(x.steal.lv, t('system.issue.steal', { v: pct(s.cpu.steal) }))
  return out.sort((a, b) => (a.lv === b.lv ? 0 : a.lv === 'crit' ? -1 : 1))
})
const overall = computed<Level>(() => (issues.value.some((i) => i.lv === 'crit') ? 'crit' : issues.value.length ? 'warn' : 'ok'))

const barStatus = (lv: Level) => (lv === 'crit' ? 'error' : lv === 'warn' ? 'warning' : 'info')

function uptime(sec: number) {
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const min = Math.floor((sec % 3600) / 60)
  if (d) return t('system.uptimeDH', { d, h })
  if (h) return t('system.uptimeHM', { h, m: min })
  return t('system.uptimeM', { m: min })
}
</script>

<template>
  <NCard size="small" :title="t('system.title')">
    <template #header-extra>
      <span v-if="info" class="state" :class="overall"><i class="dot" />{{ t(`system.overall.${overall}`) }}</span>
    </template>
    <NText v-if="failed && !info" depth="3">{{ t('system.unavailable') }}</NText>
    <NSkeleton v-else-if="!info || !m" text :repeat="4" />
    <template v-else>
      <div class="meters">
        <div class="meter">
          <div class="head"><span>{{ t('system.cpu') }}</span><b :class="m.cpu.lv">{{ pct(m.cpu.v) }}</b></div>
          <NProgress type="line" :percentage="m.cpu.v * 100" :status="barStatus(m.cpu.lv)" :show-indicator="false" :height="6" />
          <div class="sub">{{ t('system.cpuSub', { n: info.cpu.cores, io: pct(info.cpu.iowait), st: pct(info.cpu.steal) }) }}</div>
        </div>
        <div class="meter">
          <div class="head"><span>{{ t('system.memory') }}</span><b :class="m.mem.lv">{{ pct(m.mem.v) }}</b></div>
          <NProgress type="line" :percentage="m.mem.v * 100" :status="barStatus(m.mem.lv)" :show-indicator="false" :height="6" />
          <div class="sub">{{ t('system.memSub', { total: formatBytes(info.memory.total), avail: formatBytes(info.memory.available) }) }}</div>
        </div>
        <div class="meter">
          <div class="head"><span>Swap</span><b :class="m.swap.lv">{{ info.swap.total ? pct(m.swap.v) : '—' }}</b></div>
          <NProgress type="line" :percentage="m.swap.v * 100" :status="barStatus(m.swap.lv)" :show-indicator="false" :height="6" />
          <div class="sub">
            {{ info.swap.total ? t('system.swapSub', { used: formatBytes(info.swap.used), total: formatBytes(info.swap.total), io: Math.round(m.swap.io) }) : t('system.noSwap') }}
          </div>
        </div>
        <div v-if="info.disk" class="meter">
          <div class="head"><span>{{ t('system.disk') }}</span><b :class="m.disk.lv">{{ pct(m.disk.v) }}</b></div>
          <NProgress type="line" :percentage="m.disk.v * 100" :status="barStatus(m.disk.lv)" :show-indicator="false" :height="6" />
          <div class="sub">{{ t('system.diskSub', { total: formatBytes(info.disk.total), free: formatBytes(info.disk.free), inode: pct(m.inode.v) }) }}</div>
        </div>
      </div>

      <div v-if="issues.length" class="issues">
        <div v-for="i in issues" :key="i.text" :class="['issue', i.lv]"><i class="dot" />{{ i.text }}</div>
      </div>

      <button v-if="isMobile" type="button" class="facts-toggle" :aria-expanded="factsOpen" @click="toggleFacts">
        <span>{{ t('system.hostInfo') }}</span>
        <span class="peek">{{ factsOpen ? "" : `${info.hostname} · ${uptime(info.uptime)}` }}</span>
        <ChevronDown :size="16" class="chev" :class="{ open: factsOpen }" />
      </button>
      <div v-if="!isMobile || factsOpen" class="facts">
        <InfoRow :label="t('system.hostname')" mono>{{ info.hostname || '—' }}</InfoRow>
        <InfoRow :label="t('system.os')">{{ info.os || '—' }}</InfoRow>
        <InfoRow :label="t('system.kernel')" mono>{{ info.kernel }} · {{ info.arch }}</InfoRow>
        <InfoRow :label="t('system.uptime')">{{ uptime(info.uptime) }}</InfoRow>
        <InfoRow :label="t('system.load')">
          <span :class="['num', m.load.lv]">{{ info.load.map((v) => v.toFixed(2)).join(' / ') }}</span>
        </InfoRow>
        <InfoRow :label="t('system.cpuModel')">{{ info.cpu.model || '—' }}</InfoRow>
      </div>
    </template>
  </NCard>
</template>

<style scoped>
.meters {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px 24px;
}
.meter .head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  font-size: 13px;
  color: var(--viz-text-2);
  margin-bottom: 6px;
}
.meter .head b {
  font-size: 20px;
  font-weight: 600;
  color: var(--viz-text);
  font-variant-numeric: tabular-nums;
}
.sub {
  margin-top: 6px;
  font-size: 12px;
  color: var(--viz-muted);
  font-variant-numeric: tabular-nums;
}
b.warn,
.num.warn,
.issue.warn {
  color: #d08700;
}
b.crit,
.num.crit,
.issue.crit {
  color: #d03050;
}
.num {
  font-variant-numeric: tabular-nums;
}
.issues {
  margin-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
}
.issue {
  display: flex;
  align-items: center;
  gap: 8px;
}
.issue .dot,
.state .dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: currentColor;
  flex: none;
}
.facts {
  margin-top: 14px;
  padding-top: 10px;
  border-top: 1px solid var(--sbw-border);
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0 24px;
}
.state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 12px;
}
.state.ok {
  color: #18a058;
}
.state .dot {
  box-shadow: 0 0 0 3px color-mix(in srgb, currentColor 22%, transparent);
}
.state.warn {
  color: #d08700;
}
.state.crit {
  color: #d03050;
}
.facts-toggle {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  margin-top: 14px;
  padding: 10px 0 0;
  border: 0;
  border-top: 1px solid var(--sbw-border);
  background: none;
  color: var(--viz-text);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
}
.facts-toggle .peek {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  text-align: right;
  color: var(--viz-text-2);
  font-size: 12px;
}
.facts-toggle .chev {
  flex: none;
  color: var(--viz-text-2);
  transition: transform 0.15s;
}
.facts-toggle .chev.open {
  transform: rotate(180deg);
}
.facts-toggle + .facts {
  margin-top: 4px;
  padding-top: 0;
  border-top: 0;
}
@media (max-width: 900px) {
  .meters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .facts {
    grid-template-columns: minmax(0, 1fr);
  }
}
</style>
