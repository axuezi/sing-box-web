<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NDataTable, NInput, NSpace, NSwitch, NTag, NText, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, ApiError, del, post, type Conn } from '../../api'
import { formatBytes } from '../../composables/format'
import MobileCards from '../../components/MobileCards'
import { useIsMobile } from '../../composables/viewport'

const { t } = useI18n()
const isMobile = useIsMobile()
const dialog = useDialog()
const message = useMessage()

const conns = ref<Conn[]>([])
const loaded = ref(false)
const error = ref('')
const noClash = ref(false)
const filter = ref('')
const live = ref(true)
const now = ref(Date.now())

async function load() {
  try {
    const res = await api<{ connections: Conn[] }>('/connections')
    conns.value = res.connections
    error.value = ''
    noClash.value = false
  } catch (e) {
    if (e instanceof ApiError && e.code === 'no_clash_api') noClash.value = true
    else error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loaded.value = true
    now.value = Date.now()
  }
}

let timer = 0
onMounted(() => {
  load()
  timer = window.setInterval(() => live.value && load(), 2000)
})
onBeforeUnmount(() => clearInterval(timer))

const shown = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (!q) return conns.value
  return conns.value.filter((c) => [c.host, c.dest, c.source, c.client_ip ?? '', c.user, c.outbound, c.inbound, c.rule].some((v) => v.toLowerCase().includes(q)))
})

/** The client address, when it is a real one (not a local relay). */
function sourceIP(c: Conn): string {
  if (c.client_ip) return c.client_ip
  const m = /^\[?([^\]]+?)\]?:\d+$/.exec(c.source)
  const ip = m ? m[1]! : c.source
  if (!ip || ip.startsWith('127.') || ip === '::1' || ip.startsWith('::ffff:127.')) return ''
  return ip
}

function blockSource(c: Conn) {
  const ip = sourceIP(c)
  dialog.warning({
    title: t('conns.blockTitle', { ip }),
    content: t('conns.blockBody'),
    positiveText: t('conns.block'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      try {
        const res = await post<{ applied: boolean; error?: string }>('/access/rules', {
          list: 'deny', ip, note: c.user ? `${c.user} · ${c.host}` : c.host, duration: 86400,
        })
        if (res.applied) message.success(t('acl.applied'))
        else message.warning(t('acl.notApplied', { error: res.error ?? '' }), { duration: 8000 })
        load()
      } catch (e) {
        message.error(e instanceof ApiError && e.code === 'self' ? t('acl.self') : e instanceof Error ? e.message : String(e))
      }
    },
  })
}

function duration(start: string) {
  const s = Math.max(0, Math.floor((now.value - new Date(start).getTime()) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
}

async function close(id: string) {
  try {
    await del(`/connections/${encodeURIComponent(id)}`)
    conns.value = conns.value.filter((c) => c.id !== id)
  } catch (e) {
    message.error(e instanceof Error ? e.message : String(e))
  }
}

function closeAll() {
  dialog.warning({
    title: t('conns.closeAll'),
    content: t('conns.closeAllConfirm', { n: conns.value.length }),
    positiveText: t('conns.closeAll'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      await del('/connections').catch((e) => message.error(String(e)))
      load()
    },
  })
}

const columns = computed<DataTableColumns<Conn>>(() => [
  {
    title: t('conns.host'),
    key: 'host',
    minWidth: 220,
    ellipsis: { tooltip: true },
    render: (c) =>
      h('div', [
        h(NText, { strong: true }, () => c.host),
        h(NText, { depth: 3, style: 'display:block;font-size:12px' }, () => (c.dest ? `${c.network} · ${c.dest}` : c.network)),
      ]),
  },
  {
    title: t('conns.traffic'),
    key: 'traffic',
    width: 150,
    sorter: (a, b) => a.download + a.upload - (b.download + b.upload),
    render: (c) => h('span', { class: 'mono' }, `↓ ${formatBytes(c.download)}  ↑ ${formatBytes(c.upload)}`),
  },
  {
    title: t('conns.duration'),
    key: 'start',
    width: 100,
    sorter: (a, b) => new Date(a.start).getTime() - new Date(b.start).getTime(),
    render: (c) => duration(c.start),
  },
  {
    title: t('conns.user'),
    key: 'user',
    width: 120,
    sorter: (a, b) => a.user.localeCompare(b.user),
    render: (c) => (c.user ? h(NText, null, () => c.user) : h(NText, { depth: 3 }, () => t('conns.unknownUser'))),
  },
  { title: t('conns.outbound'), key: 'outbound', width: 120 },
  { title: t('conns.inbound'), key: 'inbound', width: 160, render: (c) => h(NTag, { size: 'small', bordered: false }, () => c.inbound) },
  { title: t('conns.rule'), key: 'rule', width: 150, ellipsis: { tooltip: true } },
  {
    title: t('conns.source'),
    key: 'source',
    width: 190,
    // A relayed connection shows its real client, with the relay's address on hover.
    render: (c) =>
      c.client_ip
        ? h(NText, { class: 'mono', depth: 2, title: t('conns.via', { addr: c.source }) }, () => c.client_ip)
        : h(NText, { class: 'mono', depth: 2 }, () => c.source),
  },
  {
    title: '',
    key: 'close',
    width: 130,
    render: (c) =>
      h(NSpace, { size: 2, wrap: false }, () => [
        sourceIP(c)
          ? h(NButton, { size: 'tiny', quaternary: true, type: 'warning', onClick: () => blockSource(c) }, () => t('conns.block'))
          : null,
        h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => close(c.id) }, () => t('conns.close')),
      ]),
  },
])
</script>

<template>
  <NCard size="small">
    <template #header>{{ t('conns.count', { n: shown.length }) }}</template>
    <template #header-extra>
      <NSpace :size="12" align="center">
        <NText depth="3" class="count">{{ live ? t('conns.autoRefresh') : t('conns.paused') }}</NText>
        <NSwitch v-model:value="live" size="small" />
        <NButton size="small" type="error" ghost :disabled="!conns.length" @click="closeAll">{{ t('conns.closeAll') }}</NButton>
      </NSpace>
    </template>
    <NAlert v-if="noClash" type="info" :show-icon="false">{{ t('live.noClash') }}</NAlert>
    <template v-else>
      <NAlert v-if="error" type="error" class="gap">{{ error }}</NAlert>
      <NInput v-model:value="filter" :placeholder="t('conns.filter')" clearable size="small" class="gap filter" />
      <MobileCards
        v-if="isMobile && loaded"
        :columns="columns"
        :data="shown"
        :row-key="(c: Conn) => c.id"
        title-key="host"
        actions-key="close"
      />
      <NDataTable
        v-else
        :columns="columns"
        :data="shown"
        :loading="!loaded"
        :bordered="false"
        size="small"
        :scroll-x="1340"
        :max-height="'calc(100vh - 290px)'"
        virtual-scroll
        :row-key="(c: Conn) => c.id"
      />
      <NText depth="3" class="note">{{ t('conns.sourceNote') }}</NText>
    </template>
  </NCard>
</template>

<style scoped>
.gap {
  margin-bottom: 10px;
}
.filter {
  max-width: 360px;
}
.count {
  font-size: 12px;
}
.note {
  display: block;
  font-size: 12px;
  margin-top: 8px;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  white-space: pre;
}
</style>
