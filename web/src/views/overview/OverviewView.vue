<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import { NAlert, NButton, NCard, NDataTable, NEmpty, NSkeleton, NTag, NText, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { Activity, ArrowDown, ArrowUp, CalendarDays, RefreshCw, RotateCcw } from 'lucide-vue-next'
import { api, post, type AuditEntry, type Inbound, type Status, type TrafficStats } from '../../api'
import { formatBytes, formatDateTime, formatRate } from '../../composables/format'
import { auditTagType, useAuditLabel } from '../../composables/audit'
import { useRealtime } from '../../composables/realtime'
import InfoRow from '../../components/InfoRow.vue'
import StatCard from './StatCard.vue'
import ServerStatus from './ServerStatus.vue'
import AuditRow from '../../components/AuditRow.vue'
import { useIsMobile } from '../../composables/viewport'
import LiveChart from '../../components/charts/LiveChart.vue'
import RankBars from '../../components/charts/RankBars.vue'

const { t, te } = useI18n()
const isMobile = useIsMobile()
const auditLabel = useAuditLabel()
const dialog = useDialog()
const message = useMessage()
const live = useRealtime()

const status = ref<Status | null>(null)
const traffic = ref<TrafficStats | null>(null)
const recent = ref<AuditEntry[]>([])
const error = ref('')
const serviceBusy = ref(false)

async function refresh() {
  try {
    const [s, a, tr] = await Promise.all([
      api<Status>('/status'),
      api<{ entries: AuditEntry[] }>('/audit?limit=8'),
      api<TrafficStats>('/stats/traffic?days=1').catch(() => null),
    ])
    status.value = s
    recent.value = a.entries
    traffic.value = tr
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  }
}
let timer = 0
onMounted(() => {
  refresh()
  timer = window.setInterval(refresh, 15_000)
})
onBeforeUnmount(() => clearInterval(timer))

function serviceAction(restart: boolean) {
  dialog.warning({
    title: restart ? t('service.restart') : t('service.reload'),
    content: restart ? t('service.restartConfirm') : t('service.reloadConfirm'),
    positiveText: t('common.confirm'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      serviceBusy.value = true
      try {
        await post(restart ? '/service/restart' : '/service/reload')
        message.success(t('service.done'))
      } catch (e) {
        if (e instanceof TypeError) {
          message.warning(t('apply.disconnected'), { duration: 8000 })
          setTimeout(refresh, 3000)
        } else {
          message.error(e instanceof Error ? e.message : String(e), { duration: 8000 })
        }
      } finally {
        serviceBusy.value = false
        refresh()
      }
    },
  })
}

const state = computed(() => status.value?.service.status.state ?? 'unknown')
const stateLabel = computed(() => (te(`overview.states.${state.value}`) ? t(`overview.states.${state.value}`) : state.value))
const summary = computed(() => status.value?.config.summary)

const users = computed(() => (traffic.value?.series ?? []).filter((s) => s.kind === 'user'))
const monthTotal = computed(() => users.value.reduce((n, s) => n + s.month.down + s.month.up, 0))
const todayTotal = computed(() => users.value.reduce((n, s) => n + s.today.down + s.today.up, 0))
const monthUsers = computed(() =>
  users.value.map((s) => ({ name: s.name, down: s.month.down, up: s.month.up })).filter((r) => r.down + r.up > 0),
)
const noV2ray = computed(() => traffic.value?.collector?.code === 'no_v2ray_api')

const peak = computed(() => Math.max(0, ...live.samples.value.map((s) => s.down)))

const hostPort = (host: string, port: number) => (host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`)
const inboundColumns = computed<DataTableColumns<Inbound>>(() => [
  { title: t('overview.tag'), key: 'tag', render: (r) => h(NText, { strong: true }, () => r.tag) },
  { title: t('overview.type'), key: 'type' },
  { title: t('overview.listen'), key: 'listen', render: (r) => h('span', { class: 'mono' }, hostPort(r.listen || '::', r.listen_port)) },
  { title: t('overview.users'), key: 'users', align: 'right', width: 80 },
  {
    title: t('overview.security'),
    key: 'security',
    render: (r) =>
      r.reality
        ? h(NTag, { size: 'small', type: 'info', bordered: false }, () => `Reality · ${r.server_name ?? ''}`)
        : r.tls
          ? h(NTag, { size: 'small', bordered: false }, () => 'TLS')
          : '—',
  },
])
</script>

<template>
  <div class="overview">
    <NAlert v-if="error" type="error" class="span-all">{{ error }}</NAlert>

    <!-- Host -->
    <ServerStatus class="span-all" />

    <!-- Key figures -->
    <div class="kpis span-all">
      <StatCard :icon="ArrowDown" tone="down" :label="t('live.down')" :value="formatRate(live.latest.value?.down ?? 0)"
        :sub="peak ? `${t('overview.peak')} ${formatRate(peak)}` : undefined" />
      <StatCard :icon="ArrowUp" tone="up" :label="t('live.up')" :value="formatRate(live.latest.value?.up ?? 0)" />
      <StatCard :icon="Activity" tone="green" :label="t('live.conns')" :value="String(live.latest.value?.conns ?? 0)"
        :sub="live.latest.value?.mem ? `${t('live.memory')} ${formatBytes(live.latest.value.mem)}` : undefined" />
      <StatCard :icon="CalendarDays" tone="violet" :label="t('overview.monthTraffic')" :value="noV2ray ? '—' : formatBytes(monthTotal)"
        :sub="noV2ray ? undefined : `${t('overview.today')} ${formatBytes(todayTotal)}`" />
    </div>

    <!-- Live chart -->
    <NCard size="small" class="span-2" :title="t('live.title')">
      <template #header-extra>
        <NText depth="3" class="hint">{{ t('overview.last5min') }}</NText>
      </template>
      <NText v-if="live.state.value === 'noclash'" depth="3">{{ t('live.noClash') }}</NText>
      <template v-else>
        <NAlert v-if="live.lastError.value" type="warning" :show-icon="false" class="gap">{{ live.lastError.value }}</NAlert>
        <LiveChart v-if="live.samples.value.length > 1" :samples="live.samples.value" :labels="{ down: t('live.down'), up: t('live.up') }" />
        <div v-else class="wait"><NText depth="3">{{ t('live.waiting') }}</NText></div>
      </template>
    </NCard>

    <!-- Service status -->
    <NCard size="small" class="span-1 status" :title="t('overview.service')">
      <template #header-extra>
        <span v-if="status" class="state" :class="state">
          <i class="dot" />{{ stateLabel }}
        </span>
      </template>
      <NSkeleton v-if="!status" text :repeat="6" />
      <template v-else>
        <InfoRow :label="t('overview.version')">
          <span class="ver">
            {{ status.runtime.version ?? '—' }}
            <a
              v-if="status.runtime.update"
              :href="status.runtime.update.url"
              target="_blank"
              rel="noopener noreferrer"
              class="update"
              :title="t('overview.updateHint')"
            >
              <NTag size="small" type="info" :bordered="false" round>{{ t('overview.updateAvailable', { v: status.runtime.update.version }) }}</NTag>
            </a>
            <NTag
              v-else-if="status.runtime.latest"
              size="small"
              :bordered="false"
              round
              :title="t('overview.upToDateHint', { v: status.runtime.latest.version, at: formatDateTime(status.runtime.latest.checked_at) })"
            >
              {{ t('overview.upToDate') }}
            </NTag>
          </span>
        </InfoRow>
        <InfoRow :label="t('overview.startedAt')">{{ formatDateTime(status.service.status.started_at) }}</InfoRow>
        <InfoRow :label="t('overview.restarts')">{{ status.service.status.restart_count }}</InfoRow>
        <InfoRow :label="t('overview.controller')">{{ status.service.controller }}</InfoRow>
        <NText v-if="status.service.error" type="error" class="note">{{ status.service.error }}</NText>
        <div class="divider" />
        <InfoRow :label="t('overview.configModified')">{{ formatDateTime(status.config.mod_time) }}</InfoRow>
        <InfoRow :label="t('overview.checksum')" mono>{{ status.config.sha256?.slice(0, 12) }}</InfoRow>
        <InfoRow label="Clash / V2Ray API">
          <span class="apis">
            <i class="dot" :class="summary?.clash_api ? 'ok' : 'off'" />
            <i class="dot" :class="summary?.v2ray_api ? 'ok' : 'off'" />
          </span>
        </InfoRow>
        <div class="divider" />
        <InfoRow :label="t('overview.panel')">{{ status.panel.version }} · {{ formatBytes(status.panel.rss_bytes) }}</InfoRow>
        <div v-if="status.service.controller !== 'none'" class="actions">
          <NButton size="small" secondary :loading="serviceBusy" @click="serviceAction(false)">
            <template #icon><RefreshCw :size="15" /></template>
            {{ t('service.reload') }}
          </NButton>
          <NButton size="small" secondary :disabled="serviceBusy" @click="serviceAction(true)">
            <template #icon><RotateCcw :size="15" /></template>
            {{ t('service.restart') }}
          </NButton>
        </div>
        <NText v-else depth="3" class="note">{{ t('overview.controllerNone') }}</NText>
      </template>
    </NCard>

    <!-- Inbounds -->
    <NCard size="small" class="span-2" :title="t('overview.inbounds')">
      <template #header-extra>
        <RouterLink :to="{ name: 'nodes' }" class="link">{{ t('overview.manage') }}</RouterLink>
      </template>
      <div v-if="isMobile" class="inb-list">
        <div v-for="r in summary?.inbounds ?? []" :key="r.tag" class="inb">
          <div class="inb-line">
            <NText strong>{{ r.tag }}</NText>
            <NTag size="small" :bordered="false">{{ r.type }}</NTag>
            <NTag v-if="r.reality" size="small" type="info" :bordered="false">Reality</NTag>
            <NTag v-else-if="r.tls" size="small" :bordered="false">TLS</NTag>
            <span class="inb-users">{{ t('overview.userCount', { n: r.users }) }}</span>
          </div>
          <div class="inb-line muted">
            <span class="mono">{{ hostPort(r.listen || '::', r.listen_port) }}</span>
            <span v-if="r.reality && r.server_name" class="inb-sni">{{ r.server_name }}</span>
          </div>
        </div>
        <NSkeleton v-if="!status" text :repeat="2" />
      </div>
      <NDataTable v-else :columns="inboundColumns" :data="summary?.inbounds ?? []" :loading="!status" :bordered="false" size="small" :scroll-x="560" />
      <div v-if="summary" class="facts">
        <span><NText depth="3">{{ t('overview.outbounds') }}</NText>{{ summary.outbounds.map((o) => o.tag).join(', ') || '—' }}</span>
        <span><NText depth="3">{{ t('overview.routeRules') }}</NText>{{ summary.route_rules }}</span>
        <span><NText depth="3">{{ t('overview.dnsServers') }}</NText>{{ summary.dns_servers }}</span>
        <span><NText depth="3">{{ t('overview.logLevel') }}</NText>{{ summary.log_level || 'info' }}</span>
      </div>
    </NCard>

    <!-- Users this month -->
    <NCard size="small" class="span-1" :title="t('live.monthUsers')">
      <template #header-extra>
        <RouterLink :to="{ name: 'traffic', query: { range: 'thisMonth', cmp: 'none' } }" class="link">{{ t('overview.viewAll') }}</RouterLink>
      </template>
      <NText v-if="noV2ray" depth="3">{{ t('live.noV2ray') }}</NText>
      <RankBars v-else :items="monthUsers" :empty="t('live.noTraffic')" :labels="{ down: t('live.down'), up: t('live.up') }" />
    </NCard>

    <!-- Recent activity -->
    <NCard size="small" class="span-all" :title="t('overview.recentActivity')">
      <template #header-extra>
        <RouterLink :to="{ name: 'settings', query: { tab: 'logs', log: 'audit' } }" class="link">{{ t('overview.viewAll') }}</RouterLink>
      </template>
      <NEmpty v-if="status && recent.length === 0" size="small" />
      <div v-if="isMobile">
        <AuditRow v-for="e in recent" :key="e.id" :entry="e" />
      </div>
      <div v-else class="activity-list">
        <div v-for="e in recent" :key="e.id" class="activity">
          <NTag size="small" :type="auditTagType(e.action)" :bordered="false" class="atag">{{ auditLabel(e.action) }}</NTag>
          <span class="who">{{ e.actor }}</span>
          <span class="mono muted">{{ e.ip }}</span>
          <span v-if="e.target" class="muted target">{{ e.target }}</span>
          <span class="when muted">{{ formatDateTime(e.at) }}</span>
        </div>
      </div>
    </NCard>
  </div>
</template>

<style scoped>
.overview {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}
.span-all {
  grid-column: 1 / -1;
}
.span-2 {
  grid-column: span 2;
}
.span-1 {
  grid-column: span 1;
}
.kpis {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 16px;
}
@media (max-width: 1100px) {
  .overview {
    grid-template-columns: minmax(0, 1fr);
  }
  .span-2,
  .span-1 {
    grid-column: 1 / -1;
  }
  .kpis {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}
@media (max-width: 767px) {
  .overview,
  .kpis {
    gap: 12px;
  }
}
@media (max-width: 340px) {
  .kpis {
    grid-template-columns: minmax(0, 1fr);
  }
}
.hint {
  font-size: 12px;
}
.gap {
  margin-bottom: 8px;
}
.wait {
  display: grid;
  place-items: center;
  height: 240px;
}
.state {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  font-weight: 500;
}
.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #9ca3af;
}
.state.running .dot,
.dot.ok {
  background: #18a058;
  box-shadow: 0 0 0 3px color-mix(in srgb, #18a058 22%, transparent);
}
.state.exited .dot,
.state.dead .dot {
  background: #d03050;
}
.state.running {
  color: #18a058;
}
.apis {
  display: inline-flex;
  gap: 10px;
}
.divider {
  height: 1px;
  margin: 10px 0;
  background: var(--sbw-border);
}
.note {
  display: block;
  margin-top: 8px;
  font-size: 12px;
}
.actions {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.facts {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 24px;
  margin-top: 12px;
  font-size: 13px;
}
.facts span {
  display: inline-flex;
  gap: 8px;
}
.inb {
  padding: 8px 0;
  border-bottom: 1px solid var(--sbw-border);
  font-size: 13px;
}
.inb-line {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
.inb-line + .inb-line {
  margin-top: 4px;
  font-size: 12px;
}
.inb-users {
  margin-left: auto;
  flex: none;
  color: var(--viz-muted);
  font-size: 12px;
}
.inb-sni {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.activity-list {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 32px;
}
@media (max-width: 900px) {
  .activity-list {
    grid-template-columns: minmax(0, 1fr);
  }
}
.activity {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 0;
  font-size: 13px;
  border-bottom: 1px solid var(--sbw-border);
  min-width: 0;
}
.atag {
  flex-shrink: 0;
}
.who {
  font-weight: 500;
}
.target {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.when {
  margin-left: auto;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.muted {
  color: var(--viz-muted);
}
.mono,
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.ver {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
  justify-content: flex-end;
}
.update {
  text-decoration: none;
  cursor: pointer;
}
.link {
  color: #18a058;
  font-size: 13px;
  text-decoration: none;
}
.link:hover {
  text-decoration: underline;
}
</style>
