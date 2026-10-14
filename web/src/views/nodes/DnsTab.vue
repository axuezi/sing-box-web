<script setup lang="ts">
import { computed, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NEmpty, NFormItem, NSelect, NSpace, NSwitch, NTag, NText, NTooltip, useDialog,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, del, put, type ApplyResult, type DnsResponse, type DnsRule, type DnsServerItem, type DnsSettings } from '../../api'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'
import MobileCards from '../../components/MobileCards'
import DnsServerDrawer from './DnsServerDrawer.vue'

const { t } = useI18n()
const isMobile = useIsMobile()
const dialog = useDialog()
const feedback = useApplyFeedback()

const data = ref<DnsResponse | null>(null)
const busy = ref(false)
const drawerOpen = ref(false)
const editing = ref<DnsServerItem | null>(null)
const settings = reactive<DnsSettings>({ strategy: '', final: '', disable_cache: false })

async function load() {
  try {
    data.value = await api<DnsResponse>('/dns')
    Object.assign(settings, data.value.settings)
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
onMounted(load)

const STRATEGIES = ['prefer_ipv4', 'prefer_ipv6', 'ipv4_only', 'ipv6_only']
const strategyOptions = computed(() => [
  { label: t('dns.strategies.default'), value: '' },
  ...STRATEGIES.map((v) => ({ label: t(`dns.strategies.${v}`), value: v })),
])
const allTags = computed(() => [...(data.value?.servers.map((s) => s.form.tag) ?? []), ...(data.value?.others.map((o) => o.tag) ?? [])])
const finalOptions = computed(() => [
  { label: t('dns.finalFirst', { tag: allTags.value[0] ?? '—' }), value: '' },
  ...allTags.value.map((x) => ({ label: x, value: x })),
])
const effectiveFinal = computed(() => settings.final || allTags.value[0] || '')
const settingsChanged = computed(
  () =>
    !!data.value &&
    (settings.strategy !== data.value.settings.strategy ||
      settings.final !== data.value.settings.final ||
      settings.disable_cache !== data.value.settings.disable_cache),
)

async function saveSettings() {
  busy.value = true
  try {
    const res = await put<{ result: ApplyResult }>('/dns', { settings })
    feedback.success(res.result)
  } catch (e) {
    feedback.failure(e, load)
  } finally {
    busy.value = false
    await load()
  }
}

function openNew() {
  editing.value = null
  drawerOpen.value = true
}
function openEdit(item: DnsServerItem) {
  editing.value = item
  drawerOpen.value = true
}
function confirmDelete(item: DnsServerItem) {
  dialog.warning({
    title: t('dns.deleteTitle', { tag: item.form.tag }),
    content: t('dns.deleteBody'),
    positiveText: t('common.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      busy.value = true
      try {
        const res = await del<{ result: ApplyResult }>(`/dns/servers/${encodeURIComponent(item.form.tag)}`)
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

const DEFAULT_PORT: Record<string, number> = { udp: 53, tcp: 53, tls: 853, quic: 853, https: 443, h3: 443 }
function address(i: DnsServerItem) {
  const f = i.form
  if (f.type === 'local') return h(NText, { depth: 3 }, () => t('dns.localAddr'))
  const host = f.server.includes(':') ? `[${f.server}]` : f.server
  const port = f.server_port && f.server_port !== DEFAULT_PORT[f.type] ? `:${f.server_port}` : ''
  const path = f.type === 'https' || f.type === 'h3' ? f.path || '/dns-query' : ''
  const scheme = { udp: '', tcp: 'tcp://', tls: 'tls://', https: 'https://', quic: 'quic://', h3: 'h3://' }[f.type] ?? ''
  return h('span', { class: 'mono' }, `${scheme}${host}${port}${path}`)
}

function refsCell(refs: string[]) {
  if (!refs.length) return h(NText, { depth: 3 }, () => '—')
  return h(NTooltip, { style: 'max-width: 360px' }, {
    trigger: () => h(NTag, { size: 'small', bordered: false, type: 'info' }, () => t('outbounds.refsN', { n: refs.length })),
    default: () => refs.map((r) => h('div', { class: 'mono' }, r)),
  })
}

const columns = computed<DataTableColumns<DnsServerItem>>(() => [
  {
    title: t('outbounds.tag'),
    key: 'tag',
    minWidth: 130,
    render: (i) =>
      h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
        h(NText, { strong: true }, () => i.form.tag),
        i.form.tag === effectiveFinal.value ? h(NTag, { size: 'tiny', type: 'success', bordered: false }, () => t('outbounds.default')) : null,
      ]),
  },
  { title: t('outbounds.type'), key: 'type', width: 90, render: (i) => h(NTag, { size: 'small', bordered: false }, () => i.form.type) },
  { title: t('dns.address'), key: 'address', minWidth: 220, render: address },
  {
    title: t('outbounds.detour'),
    key: 'detour',
    width: 110,
    render: (i) => (i.form.detour ? i.form.detour : h(NText, { depth: 3 }, () => '—')),
  },
  {
    title: t('dns.resolver'),
    key: 'resolver',
    width: 120,
    render: (i) => (i.form.resolver ? i.form.resolver : h(NText, { depth: 3 }, () => '—')),
  },
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

const actionLabel = (r: DnsRule) => (['route', 'reject', 'predefined', 'route-options'].includes(r.action) ? t(`dns.actions.${r.action}`) : r.action)
const legacyRules = computed(() => data.value?.rules.filter((r) => r.legacy_strategy).length ?? 0)
</script>

<template>
  <NCard size="small" :title="t('dns.settings')" class="block">
    <div v-if="data" class="settings">
      <NFormItem :label="t('dns.final')" :show-feedback="false" class="grow">
        <NSelect v-model:value="settings.final" :options="finalOptions" />
      </NFormItem>
      <NFormItem :label="t('dns.strategy')" :show-feedback="false" class="grow">
        <NSelect v-model:value="settings.strategy" :options="strategyOptions" />
      </NFormItem>
      <NFormItem :label="t('dns.cache')" :show-feedback="false" class="narrow">
        <NSpace align="center" :size="8" class="switch">
          <NSwitch :value="!settings.disable_cache" size="small" @update:value="(v: boolean) => (settings.disable_cache = !v)" />
          <span>{{ settings.disable_cache ? t('dns.cacheOff') : t('dns.cacheOn') }}</span>
        </NSpace>
      </NFormItem>
      <NButton type="primary" class="save" :loading="busy" :disabled="!settingsChanged" @click="saveSettings">
        {{ t('inbounds.saveApply') }}
      </NButton>
    </div>
    <NText depth="3" class="hint">{{ t('dns.settingsHint') }}</NText>
  </NCard>

  <NCard size="small" class="block">
    <template #header>{{ data ? t('dns.count', { n: data.servers.length + data.others.length }) : t('dns.servers') }}</template>
    <template #header-extra>
      <NButton type="primary" size="small" :disabled="busy || !data" @click="openNew">+ {{ t('dns.add') }}</NButton>
    </template>
    <MobileCards
      v-if="isMobile && data"
      :columns="columns"
      :data="data.servers"
      :row-key="(i: DnsServerItem) => i.form.tag"
      title-key="tag"
      actions-key="actions"
      :empty="t('dns.empty')"
    />
    <NDataTable
      v-else
      :columns="columns"
      :data="data?.servers ?? []"
      :loading="!data"
      :bordered="false"
      size="small"
      :scroll-x="900"
      :row-key="(i: DnsServerItem) => i.form.tag"
    >
      <template #empty><NEmpty :description="t('dns.empty')" /></template>
    </NDataTable>
    <template v-if="data && data.others.length">
      <NText depth="3" class="hint">{{ t('dns.othersHint') }}</NText>
      <NSpace :size="8" class="others">
        <NTag v-for="o in data.others" :key="o.tag" :bordered="false" :type="o.legacy ? 'warning' : 'default'">
          {{ o.tag }} · {{ o.legacy ? t('dns.legacy') : o.type }}
        </NTag>
      </NSpace>
    </template>
  </NCard>

  <NCard size="small" :title="t('dns.rules')" class="block">
    <NAlert v-if="legacyRules" type="warning" :show-icon="false" class="gap">{{ t('dns.legacyStrategy', { n: legacyRules }) }}</NAlert>
    <NText v-if="data && !data.rules.length" depth="3">{{ t('dns.noRules') }}</NText>
    <ol v-if="data && data.rules.length" class="rules">
      <li v-for="(r, i) in data.rules" :key="i" class="rule">
        <div class="when">
          <NTag v-if="r.invert" size="tiny" :bordered="false" type="warning">{{ t('dns.invert') }}</NTag>
          <template v-for="c in r.conditions" :key="c.key">
            <NTooltip v-if="c.key !== 'logical'" :disabled="c.values.length < 2" style="max-width: 360px">
              <template #trigger>
                <span class="cond">
                  <span class="key">{{ c.key }}</span>
                  <span class="mono">{{ c.values.length > 1 ? t('dns.nValues', { first: c.values[0], n: c.values.length }) : c.values[0] }}</span>
                </span>
              </template>
              <div v-for="v in c.values" :key="v" class="mono">{{ v }}</div>
            </NTooltip>
            <span v-else class="cond">{{ t('dns.logical', { mode: c.values[0], n: c.values[1] }) }}</span>
          </template>
          <NText v-if="!r.conditions.length" depth="3">{{ t('dns.always') }}</NText>
        </div>
        <div class="then">
          → <b>{{ actionLabel(r) }}</b>
          <span v-if="r.server" class="srv">{{ r.server }}</span>
          <NText v-if="r.strategy" depth="3"> · {{ t(`dns.strategies.${r.strategy}`) }}</NText>
          <NTag v-if="r.legacy_strategy" size="tiny" :bordered="false" type="warning" class="dep">{{ t('dns.deprecated') }}</NTag>
        </div>
      </li>
    </ol>
    <NText depth="3" class="hint">{{ t('dns.rulesHint') }}</NText>
  </NCard>

  <DnsServerDrawer
    v-model:show="drawerOpen"
    :item="editing"
    :outbounds="data?.outbounds ?? []"
    :servers="allTags"
    @saved="load"
  />
</template>

<style scoped>
.block + .block {
  margin-top: 16px;
}
.gap {
  margin-bottom: 12px;
}
.settings {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 12px 16px;
}
.grow {
  flex: 1 1 200px;
}
.narrow {
  flex: 0 1 auto;
}
.switch {
  height: 34px;
}
.save {
  flex: none;
}
.hint {
  display: block;
  font-size: 12px;
  margin-top: 10px;
}
.others {
  margin-top: 8px;
}
.rules {
  margin: 0;
  padding-left: 22px;
}
.rule {
  padding: 8px 0;
  border-bottom: 1px solid var(--sbw-border);
  font-size: 13px;
}
.rule:last-child {
  border-bottom: 0;
}
.when {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 10px;
}
.cond {
  display: inline-flex;
  gap: 6px;
  align-items: baseline;
  min-width: 0;
  overflow-wrap: anywhere;
}
.key {
  color: var(--viz-muted);
  font-size: 12px;
}
.then {
  margin-top: 4px;
}
.srv {
  margin-left: 4px;
}
.dep {
  margin-left: 6px;
}
.mono,
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
