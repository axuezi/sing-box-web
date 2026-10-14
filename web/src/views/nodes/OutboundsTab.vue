<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NEmpty, NSpace, NTag, NText, NTooltip, useDialog } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, del, type ApplyResult, type OutboundItem, type OutboundsResponse } from '../../api'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'
import MobileCards from '../../components/MobileCards'
import OutboundFormDrawer from './OutboundFormDrawer.vue'

const { t } = useI18n()
const isMobile = useIsMobile()
const dialog = useDialog()
const feedback = useApplyFeedback()

const data = ref<OutboundsResponse | null>(null)
const busy = ref(false)
const drawerOpen = ref(false)
const editing = ref<OutboundItem | null>(null)

async function load() {
  try {
    data.value = await api<OutboundsResponse>('/outbounds')
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
onMounted(load)

function openNew() {
  editing.value = null
  drawerOpen.value = true
}
function openEdit(item: OutboundItem) {
  editing.value = item
  drawerOpen.value = true
}

function confirmDelete(item: OutboundItem) {
  dialog.warning({
    title: t('outbounds.deleteTitle', { tag: item.form.tag }),
    content: t('outbounds.deleteBody'),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      busy.value = true
      try {
        const res = await del<{ result: ApplyResult }>(`/outbounds/${encodeURIComponent(item.form.tag)}`)
        feedback.success(res.result)
      } catch (e) {
        feedback.failure(e, load)
      } finally {
        busy.value = false
        await load()
      }
    },
  })
}

const hostPort = (host: string, port: number) => (host.includes(':') ? `[${host}]:${port}` : `${host}:${port}`)

/** Where traffic leaves to: the remote server, or for direct the source address. */
function target(i: OutboundItem) {
  const f = i.form
  if (f.type !== 'direct') return h('span', { class: 'mono' }, hostPort(f.server, f.server_port))
  const binds = [f.inet4_bind_address, f.inet6_bind_address, f.bind_interface].filter(Boolean)
  return binds.length
    ? h('span', { class: 'mono' }, `${t('outbounds.from')} ${binds.join(' / ')}`)
    : h(NText, { depth: 3 }, () => t('outbounds.directDefault'))
}

function resolver(i: OutboundItem) {
  const f = i.form
  if (!f.resolver) return h(NText, { depth: 3 }, () => t('outbounds.resolverDefault'))
  return f.resolver_strategy ? `${f.resolver} · ${t(`dns.strategies.${f.resolver_strategy}`)}` : f.resolver
}

/** "Used by N" with the places in a tooltip; nothing when unused. */
function refsCell(refs: string[]) {
  if (!refs.length) return h(NText, { depth: 3 }, () => '—')
  return h(NTooltip, { style: 'max-width: 360px' }, {
    trigger: () => h(NTag, { size: 'small', bordered: false, type: 'info' }, () => t('outbounds.refsN', { n: refs.length })),
    default: () => refs.map((r) => h('div', { class: 'mono' }, r)),
  })
}

const columns = computed<DataTableColumns<OutboundItem>>(() => [
  {
    title: t('outbounds.tag'),
    key: 'tag',
    minWidth: 130,
    render: (i) =>
      h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        h(NText, { strong: true }, () => i.form.tag),
        i.form.tag === data.value?.route_final ? h(NTag, { size: 'tiny', type: 'success', bordered: false }, () => t('outbounds.default')) : null,
      ]),
  },
  { title: t('outbounds.type'), key: 'type', width: 110, render: (i) => h(NTag, { size: 'small', bordered: false }, () => i.form.type) },
  { title: t('outbounds.target'), key: 'target', minWidth: 190, render: target },
  {
    title: t('outbounds.detour'),
    key: 'detour',
    width: 110,
    render: (i) => (i.form.detour ? i.form.detour : h(NText, { depth: 3 }, () => '—')),
  },
  { title: t('outbounds.resolver'), key: 'resolver', minWidth: 150, render: resolver },
  { title: t('outbounds.refs'), key: 'refs', width: 100, render: (i) => refsCell(i.refs) },
  {
    title: t('common.actions'),
    key: 'actions',
    width: 140,
    fixed: 'right',
    render: (i) =>
      h(NSpace, { size: 6, wrap: false }, () => [
        h(NButton, { size: 'small', type: 'primary', ghost: true, disabled: busy.value, onClick: () => openEdit(i) }, () => t('common.edit')),
        i.refs.length
          ? h(NTooltip, null, {
              trigger: () => h(NButton, { size: 'small', quaternary: true, disabled: true }, () => t('common.delete')),
              default: () => t('outbounds.inUse'),
            })
          : h(NButton, { size: 'small', quaternary: true, type: 'error', disabled: busy.value, onClick: () => confirmDelete(i) }, () =>
              t('common.delete')),
      ]),
  },
])
</script>

<template>
  <NCard size="small">
    <template #header>
      <NSpace v-if="data" :size="12" align="center">
        <span>{{ t('outbounds.count', { n: data.outbounds.length + data.others.length }) }}</span>
        <NText depth="3" class="sub">{{ t('outbounds.finalIs', { tag: data.route_final || '—' }) }}</NText>
      </NSpace>
    </template>
    <template #header-extra>
      <NButton type="primary" size="small" :disabled="busy || !data" @click="openNew">+ {{ t('outbounds.add') }}</NButton>
    </template>
    <MobileCards
      v-if="isMobile && data"
      :columns="columns"
      :data="data.outbounds"
      :row-key="(i: OutboundItem) => i.form.tag"
      title-key="tag"
      actions-key="actions"
      :empty="t('outbounds.empty')"
    />
    <NDataTable
      v-else
      :columns="columns"
      :data="data?.outbounds ?? []"
      :loading="!data"
      :bordered="false"
      size="small"
      :scroll-x="980"
      :row-key="(i: OutboundItem) => i.form.tag"
    >
      <template #empty><NEmpty :description="t('outbounds.empty')" /></template>
    </NDataTable>
    <NText depth="3" class="hint">{{ t('outbounds.routeHint') }}</NText>
  </NCard>

  <NCard v-if="data && data.others.length" size="small" :title="t('outbounds.others')" class="others">
    <NText depth="3" class="hint top">{{ t('outbounds.othersHint') }}</NText>
    <NSpace :size="8">
      <NTag v-for="o in data.others" :key="o.tag" :bordered="false">
        {{ o.tag }} · {{ o.type }}<template v-if="o.refs.length"> · {{ t('outbounds.refsN', { n: o.refs.length }) }}</template>
      </NTag>
    </NSpace>
  </NCard>

  <OutboundFormDrawer
    v-model:show="drawerOpen"
    :item="editing"
    :tags="data?.tags ?? []"
    :dns-servers="data?.dns_servers ?? []"
    @saved="load"
  />
</template>

<style scoped>
.sub {
  font-size: 12px;
  font-weight: normal;
}
.others {
  margin-top: 16px;
}
.hint {
  display: block;
  font-size: 12px;
  margin-top: 10px;
}
.hint.top {
  margin: 0 0 10px;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
