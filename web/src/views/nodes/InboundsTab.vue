<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NDataTable, NEmpty, NInput, NSpace, NTag, NText, useDialog, useMessage } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, del, put, type ApplyResult, type InboundItem, type InboundsResponse } from '../../api'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import InboundFormDrawer from './InboundFormDrawer.vue'
import MobileCards from '../../components/MobileCards'
import { useIsMobile } from '../../composables/viewport'

const { t } = useI18n()
const isMobile = useIsMobile()
const dialog = useDialog()
const feedback = useApplyFeedback()

const data = ref<InboundsResponse | null>(null)
const busy = ref(false)
const drawerOpen = ref(false)
const message = useMessage()

// The address clients connect to; each inbound can override it.
const host = ref('')
const hostBusy = ref(false)
async function saveHost() {
  hostBusy.value = true
  try {
    const cur = await api<{ settings: { public_host: string; inbounds: Record<string, unknown> } }>('/settings/subscription')
    await put('/settings/subscription', { ...cur.settings, public_host: host.value.trim() })
    message.success(t('inbounds.hostSaved'))
    await load()
  } catch (e) {
    feedback.failure(e)
  } finally {
    hostBusy.value = false
  }
}
const editing = ref<InboundItem | null>(null)

async function load() {
  try {
    data.value = await api<InboundsResponse>('/inbounds')
    host.value = data.value.public_host
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
onMounted(load)

function openNew() {
  editing.value = null
  drawerOpen.value = true
}

function openEdit(item: InboundItem) {
  editing.value = item
  drawerOpen.value = true
}

function confirmDelete(item: InboundItem) {
  dialog.warning({
    title: t('inbounds.deleteTitle', { tag: item.form.tag }),
    content: t('inbounds.deleteBody'),
    positiveText: t('inbounds.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      busy.value = true
      try {
        const res = await del<{ result: ApplyResult }>(`/inbounds/${encodeURIComponent(item.form.tag)}`)
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

const security = (i: InboundItem) => {
  const m = i.form.tls.mode
  return m === 'reality' ? t('inbounds.secReality') : m === 'tls' ? t('inbounds.secTls') : t('inbounds.secNone')
}

const listenText = (listen: string, port: number) => `${listen.includes(':') ? `[${listen || '::'}]` : listen}:${port}`

const columns = computed<DataTableColumns<InboundItem>>(() => [
  {
    title: t('inbounds.tag'),
    key: 'tag',
    minWidth: 120,
    render: (i) => h(NText, { strong: true }, () => i.form.tag),
  },
  {
    title: t('inbounds.type'),
    key: 'type',
    width: 105,
    render: (i) => h(NTag, { size: 'small', bordered: false }, () => i.form.type),
  },
  {
    title: t('inbounds.listen'),
    key: 'listen',
    width: 160,
    render: (i) => h('span', { class: 'mono' }, listenText(i.form.listen || '::', i.form.listen_port)),
  },
  {
    title: t('inbounds.security'),
    key: 'security',
    width: 90,
    render: (i) =>
      h(NTag, { size: 'small', bordered: false, type: i.form.tls.mode === 'none' ? 'warning' : 'success' }, () => security(i)),
  },
  {
    title: t('inbounds.sni'),
    key: 'sni',
    minWidth: 140,
    ellipsis: { tooltip: true },
    render: (i) => i.form.tls.server_name || h(NText, { depth: 3 }, () => '—'),
  },
  {
    title: t('inbounds.exportName'),
    key: 'name',
    minWidth: 120,
    render: (i) =>
      i.export.hidden
        ? h(NTag, { size: 'small', bordered: false }, () => t('inbounds.notExported'))
        : i.export.name || h(NText, { depth: 3 }, () => i.form.tag),
  },
  {
    title: t('inbounds.clientPort'),
    key: 'client',
    width: 95,
    render: (i) =>
      i.export.port ? h('span', { class: 'mono' }, String(i.export.port)) : h(NText, { depth: 3 }, () => String(i.form.listen_port)),
  },
  { title: t('inbounds.users'), key: 'users', width: 80, render: (i) => String(i.users) },
  {
    title: t('inbounds.actions'),
    key: 'actions',
    width: 140,
    fixed: 'right',
    render: (i) =>
      h(NSpace, { size: 6, wrap: false }, () => [
        h(NButton, { size: 'small', type: 'primary', ghost: true, disabled: busy.value, onClick: () => openEdit(i) }, () => t('common.edit')),
        h(NButton, { size: 'small', quaternary: true, type: 'error', disabled: busy.value, onClick: () => confirmDelete(i) }, () =>
          t('inbounds.delete')),
      ]),
  },
])
</script>

<template>
  <NCard size="small" class="host-card">
    <div class="host">
      <div class="host-label">
        <b>{{ t('inbounds.publicHost') }}</b>
        <NText depth="3" class="hint">{{ t('inbounds.publicHostHint') }}</NText>
      </div>
      <div class="host-input">
        <NInput v-model:value="host" placeholder="203.0.113.10 / vpn.example.com" @keyup.enter="saveHost" />
        <NButton type="primary" :loading="hostBusy" :disabled="!data || host.trim() === data.public_host" @click="saveHost">
          {{ t('common.save') }}
        </NButton>
      </div>
    </div>
  </NCard>
  <NCard size="small">
    <template #header>{{ data ? t('inbounds.count', { n: data.inbounds.length }) : '' }}</template>
    <template #header-extra>
      <NButton type="primary" size="small" :disabled="busy || !data" @click="openNew">+ {{ t('inbounds.add') }}</NButton>
    </template>
    <MobileCards
      v-if="isMobile && data"
      :columns="columns"
      :data="data.inbounds"
      :row-key="(i: InboundItem) => i.form.tag"
      title-key="tag"
      actions-key="actions"
      :empty="t('inbounds.empty')"
    />
    <NDataTable
      v-else
      :columns="columns"
      :data="data?.inbounds ?? []"
      :loading="!data"
      :bordered="false"
      size="small"
      :scroll-x="1040"
      :row-key="(i: InboundItem) => i.form.tag"
    >
      <template #empty><NEmpty :description="t('inbounds.empty')" /></template>
    </NDataTable>
  </NCard>

  <NCard v-if="data && data.others.length" size="small" :title="t('inbounds.others')" class="others">
    <NText depth="3" class="hint">{{ t('inbounds.othersHint') }}</NText>
    <NSpace :size="8">
      <NTag v-for="o in data.others" :key="o.tag" :bordered="false">
        {{ o.tag }} · {{ o.type }}<template v-if="o.listen_port"> · {{ listenText(o.listen || '::', o.listen_port) }}</template>
      </NTag>
    </NSpace>
  </NCard>

  <InboundFormDrawer
    v-model:show="drawerOpen"
    :item="editing"
    :public-host="data?.public_host ?? ''"
    :user-count="data?.user_count ?? 0"
    @saved="load"
  />
</template>

<style scoped>
.host-card {
  margin-bottom: 16px;
}
.host {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 24px;
  align-items: center;
}
.host-label {
  flex: 1 1 260px;
}
.host-label .hint {
  margin: 2px 0 0;
}
.host-input {
  display: flex;
  gap: 8px;
  flex: 1 1 320px;
}
.others {
  margin-top: 16px;
}
.hint {
  display: block;
  font-size: 12px;
  margin-bottom: 10px;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
