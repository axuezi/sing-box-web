<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCard, NDataTable, NEmpty, NInput, NSelect, NSpace, NSwitch, NTag, NText, useDialog, useMessage,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, ApiError, del, post, put, type AccessRule, type AccessState, type AccessTarget, type F2BBan } from '../../api'
import { formatDateTime } from '../../composables/format'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import MobileCards from '../../components/MobileCards'
import { useIsMobile } from '../../composables/viewport'

const { t } = useI18n()
const isMobile = useIsMobile()
const message = useMessage()
const dialog = useDialog()
const feedback = useApplyFeedback()

const state = ref<AccessState | null>(null)
const bans = ref<F2BBan[] | null>(null)
const jails = ref<string[]>([])
const busy = ref(false)
const now = ref(Date.now())

async function load() {
  try {
    state.value = await api<AccessState>('/access')
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
async function loadBans() {
  try {
    const res = await api<{ jails: string[]; bans: F2BBan[] }>('/access/bans')
    bans.value = res.bans
    jails.value = res.jails ?? []
    if (!banForm.jail && jails.value.length) banForm.jail = jails.value[0]!
  } catch (e) {
    bans.value = []
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
onMounted(async () => {
  await load()
  if (state.value?.available) loadBans()
})
const timer = setInterval(() => {
  now.value = Date.now()
  // While the allowlist awaits confirmation, follow the countdown closely.
  if (confirmLeft.value !== null && confirmLeft.value <= 0) load()
}, 1000)
const refresher = setInterval(() => {
  if (!busy.value && document.visibilityState === 'visible') load()
}, 30_000)
onBeforeUnmount(() => {
  clearInterval(timer)
  clearInterval(refresher)
})

/** Reports whether the host took the change; it is saved either way. */
function applied(res: { applied?: boolean; error?: string }) {
  if (res.applied === false) message.warning(t('acl.notApplied', { error: res.error ?? '' }), { duration: 8000 })
  else message.success(t('acl.applied'))
}

async function run<T>(fn: () => Promise<T>): Promise<T | undefined> {
  busy.value = true
  try {
    return await fn()
  } catch (e) {
    if (e instanceof ApiError && e.code === 'self') message.error(t('acl.self'))
    else feedback.failure(e)
  } finally {
    busy.value = false
  }
}

// --- rules ---
const rules = computed(() => state.value?.rules ?? [])
const denyRules = computed(() => rules.value.filter((r) => r.list === 'deny'))
const allowRules = computed(() => rules.value.filter((r) => r.list === 'allow'))

const DURATIONS = [3600, 86400, 7 * 86400, 0]
const durationOptions = computed(() =>
  DURATIONS.map((d) => ({ value: d, label: d ? t(`acl.dur.${d}`) : t('acl.permanent') })),
)
const denyForm = reactive({ ip: '', note: '', duration: 86400 })
const allowForm = reactive({ ip: '', note: '' })

async function addRule(list: 'deny' | 'allow') {
  const f = list === 'deny' ? denyForm : allowForm
  if (!f.ip.trim()) return
  const res = await run(() =>
    post<{ applied: boolean; error?: string }>('/access/rules', {
      list,
      ip: f.ip.trim(),
      note: f.note,
      duration: list === 'deny' ? denyForm.duration : 0,
    }),
  )
  if (res) {
    applied(res)
    f.ip = ''
    f.note = ''
    await load()
  }
}

function removeRule(r: AccessRule) {
  dialog.warning({
    title: t('acl.removeTitle', { ip: r.cidr }),
    content: r.list === 'deny' ? t('acl.removeDeny') : t('acl.removeAllow'),
    positiveText: t('acl.remove'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      const res = await run(() => del<{ applied: boolean; error?: string }>(`/access/rules/${r.id}`))
      if (res) applied(res)
      await load()
    },
  })
}

function left(iso?: string): string {
  if (!iso) return t('acl.permanent')
  const s = Math.max(0, Math.floor((new Date(iso).getTime() - now.value) / 1000))
  if (s < 3600) return t('acl.leftMin', { n: Math.max(1, Math.ceil(s / 60)) })
  if (s < 86400) return t('acl.leftHour', { n: Math.floor(s / 3600) })
  return t('acl.leftDay', { n: Math.floor(s / 86400) })
}

const ruleColumns = (list: 'deny' | 'allow') =>
  computed<DataTableColumns<AccessRule>>(() => [
    { title: t('acl.ip'), key: 'cidr', minWidth: 170, render: (r) => h('span', { class: 'mono' }, r.cidr) },
    { title: t('acl.note'), key: 'note', minWidth: 140, render: (r) => r.note || h(NText, { depth: 3 }, () => '—') },
    ...(list === 'deny'
      ? [{ title: t('acl.expires'), key: 'expires_at', width: 120, render: (r: AccessRule) => left(r.expires_at) }]
      : []),
    { title: t('acl.added'), key: 'created_at', width: 170, render: (r) => formatDateTime(r.created_at) },
    {
      title: '',
      key: 'actions',
      width: 80,
      render: (r) =>
        h(NButton, { size: 'tiny', quaternary: true, type: 'error', disabled: busy.value, onClick: () => removeRule(r) }, () =>
          t('acl.remove')),
    },
  ])
const denyColumns = ruleColumns('deny')
const allowColumns = ruleColumns('allow')

// --- allowlist switch ---
const allowOn = computed(() => !!state.value?.allowlist?.on)
const outside = computed(() => state.value?.allowlist?.outside ?? [])
const confirmLeft = computed(() => {
  const by = state.value?.allowlist?.confirm_by
  if (!by || !allowOn.value) return null
  const d = new Date(by).getTime()
  if (d < Date.UTC(2000, 0)) return null
  return Math.max(0, Math.ceil((d - now.value) / 1000))
})

function toggleAllowlist(on: boolean) {
  if (!on) {
    run(() => put<{ applied: boolean; error?: string }>('/access/allowlist', { on: false })).then((res) => {
      if (res) applied(res)
      load()
    })
    return
  }
  dialog.warning({
    title: t('acl.allowOnTitle'),
    content: () =>
      h('div', [
        h('p', t('acl.allowOnBody', { n: allowRules.value.length })),
        outside.value.length
          ? h(NAlert, { type: 'warning', showIcon: false }, () =>
              t('acl.allowOnOutside', { n: outside.value.length, ips: outside.value.slice(0, 5).map((s) => s.ip).join(', ') }))
          : null,
        h(NText, { depth: 3, style: 'display:block;margin-top:8px;font-size:12px' }, () => t('acl.allowOnConfirmHint')),
      ]),
    positiveText: t('acl.turnOn'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      const res = await run(() => put<{ applied: boolean; error?: string }>('/access/allowlist', { on: true }))
      if (res) applied(res)
      await load()
    },
  })
}

async function confirmAllowlist() {
  const res = await run(async () => (await post('/access/allowlist/confirm'), true))
  if (res) message.success(t('acl.confirmed'))
  await load()
}

// --- targets ---
const targetColumns = computed<DataTableColumns<AccessTarget>>(() => [
  { title: t('acl.inbound'), key: 'tag', minWidth: 130, render: (x) => h(NText, { strong: true }, () => x.tag) },
  { title: t('acl.protocol'), key: 'type', width: 110, render: (x) => h(NTag, { size: 'small', bordered: false }, () => x.type) },
  {
    title: t('acl.port'),
    key: 'port',
    width: 140,
    render: (x) => h('span', { class: 'mono' }, `${x.networks.join('/').toUpperCase()} ${x.port}`),
  },
  {
    title: t('acl.enforcedBy'),
    key: 'by',
    minWidth: 220,
    render: (x) =>
      x.by === 'none'
        ? h('div', [
            h(NTag, { size: 'small', bordered: false, type: 'warning' }, () => t('acl.byNone')),
            h(NText, { depth: 3, style: 'display:block;font-size:12px;margin-top:2px' }, () => t('acl.reasonLoopback')),
          ])
        : h(NTag, { size: 'small', bordered: false, type: 'success' }, () => (x.by === 'firewall' ? t('acl.byFirewall') : t('acl.byHAProxy'))),
  },
])

// --- fail2ban ---
const banFilter = ref('')
const banJail = ref<string | null>(null)
const banForm = reactive({ jail: '', ip: '' })
const shownBans = computed(() =>
  (bans.value ?? []).filter((b) => (!banJail.value || b.jail === banJail.value) && b.ip.includes(banFilter.value.trim())),
)
const jailOptions = computed(() => jails.value.map((j) => ({ label: j, value: j })))
const jailFilterOptions = computed(() => [{ label: t('acl.allJails'), value: '' }, ...jailOptions.value])

async function banAction(action: 'ban' | 'unban', jail: string, ip: string) {
  const res = await run(async () => (await post('/access/bans', { action, jail, ip }), true))
  if (res) {
    message.success(action === 'ban' ? t('acl.banned') : t('acl.unbanned'))
    if (action === 'ban') banForm.ip = ''
    await loadBans()
  }
}

const banColumns = computed<DataTableColumns<F2BBan>>(() => [
  { title: t('acl.ip'), key: 'ip', minWidth: 150, render: (b) => h('span', { class: 'mono' }, b.ip) },
  { title: t('acl.jail'), key: 'jail', width: 130, render: (b) => h(NTag, { size: 'small', bordered: false }, () => b.jail) },
  { title: t('acl.since'), key: 'since', width: 170, render: (b) => formatDateTime(b.since) },
  { title: t('acl.expires'), key: 'until', width: 120, render: (b) => (b.until ? left(b.until) : '—') },
  {
    title: '',
    key: 'actions',
    width: 80,
    render: (b) =>
      h(NButton, { size: 'tiny', quaternary: true, type: 'primary', disabled: busy.value, onClick: () => banAction('unban', b.jail, b.ip) }, () =>
        t('acl.unban')),
  },
])

const blocked = computed(() => {
  const s = state.value?.stats ?? {}
  return (s.deny ?? 0) + (s.allow ?? 0)
})
</script>

<template>
  <NAlert v-if="state && !state.available" type="warning" :title="t('acl.unavailable')" class="gap">
    <template v-if="state.code !== 'no_ctl'">{{ state.error }}</template>
    <div class="hint">{{ t('acl.unavailableHint') }}</div>
  </NAlert>

  <template v-if="state?.available">
    <NAlert v-if="confirmLeft !== null" type="warning" class="gap confirm">
      <div class="confirm-row">
        <span>{{ t('acl.confirmBanner', { n: confirmLeft }) }}</span>
        <NSpace :size="8">
          <NButton type="primary" size="small" :loading="busy" @click="confirmAllowlist">{{ t('acl.keepOn') }}</NButton>
          <NButton size="small" :disabled="busy" @click="toggleAllowlist(false)">{{ t('acl.turnOff') }}</NButton>
        </NSpace>
      </div>
    </NAlert>

    <NCard size="small" :title="t('acl.where')" class="gap">
      <template #header-extra>
        <NText depth="3" class="small">
          <template v-if="state.last?.at">
            {{ state.last.ok ? t('acl.lastOk', { at: formatDateTime(state.last.at) }) : t('acl.lastFail', { at: formatDateTime(state.last.at) }) }}
          </template>
          · {{ t('acl.blockedCount', { n: blocked }) }}
        </NText>
      </template>
      <MobileCards v-if="isMobile" :columns="targetColumns" :data="state.targets ?? []" :row-key="(x: AccessTarget) => x.tag" title-key="tag" />
      <NDataTable v-else :columns="targetColumns" :data="state.targets ?? []" :bordered="false" size="small" :row-key="(x: AccessTarget) => x.tag" />
      <NAlert v-if="state.last && !state.last.ok" type="error" :show-icon="false" class="top">{{ state.last.output }}</NAlert>
      <NText depth="3" class="hint">{{ t('acl.whereHint') }}</NText>
    </NCard>

    <NCard size="small" :title="t('acl.denyTitle')" class="gap">
      <template #header-extra><NText depth="3" class="small">{{ t('acl.count', { n: denyRules.length }) }}</NText></template>
      <div class="form">
        <NInput v-model:value="denyForm.ip" :placeholder="t('acl.ipPlaceholder')" class="ip" @keyup.enter="addRule('deny')" />
        <NInput v-model:value="denyForm.note" :placeholder="t('acl.notePlaceholder')" class="note" />
        <NSelect v-model:value="denyForm.duration" :options="durationOptions" class="dur" />
        <NButton type="error" :disabled="!denyForm.ip.trim()" :loading="busy" @click="addRule('deny')">{{ t('acl.block') }}</NButton>
      </div>
      <NText depth="3" class="hint">{{ t('acl.denyHint') }}</NText>
      <MobileCards v-if="isMobile" :columns="denyColumns" :data="denyRules" :row-key="(r: AccessRule) => r.id" title-key="cidr" actions-key="actions" :empty="t('acl.noDeny')" />
      <NDataTable v-else :columns="denyColumns" :data="denyRules" :bordered="false" size="small" :row-key="(r: AccessRule) => r.id">
        <template #empty><NEmpty :description="t('acl.noDeny')" /></template>
      </NDataTable>
    </NCard>

    <NCard size="small" :title="t('acl.allowTitle')" class="gap">
      <template #header-extra>
        <NSpace align="center" :size="8">
          <NText depth="3" class="small">{{ allowOn ? t('acl.allowOn') : t('acl.allowOff') }}</NText>
          <NSwitch :value="allowOn" :loading="busy" :disabled="!allowOn && !allowRules.length" @update:value="toggleAllowlist" />
        </NSpace>
      </template>
      <NAlert v-if="allowOn && outside.length" type="warning" :show-icon="false" class="gap-s">
        {{ t('acl.outsideNow', { n: outside.length, ips: outside.slice(0, 5).map((s) => s.ip).join(', ') }) }}
      </NAlert>
      <div class="form">
        <NInput v-model:value="allowForm.ip" :placeholder="t('acl.ipPlaceholder')" class="ip" @keyup.enter="addRule('allow')" />
        <NInput v-model:value="allowForm.note" :placeholder="t('acl.notePlaceholder')" class="note" />
        <NButton type="primary" :disabled="!allowForm.ip.trim()" :loading="busy" @click="addRule('allow')">{{ t('acl.allowAdd') }}</NButton>
      </div>
      <NText depth="3" class="hint">{{ t('acl.allowHint') }}</NText>
      <MobileCards v-if="isMobile" :columns="allowColumns" :data="allowRules" :row-key="(r: AccessRule) => r.id" title-key="cidr" actions-key="actions" :empty="t('acl.noAllow')" />
      <NDataTable v-else :columns="allowColumns" :data="allowRules" :bordered="false" size="small" :row-key="(r: AccessRule) => r.id">
        <template #empty><NEmpty :description="t('acl.noAllow')" /></template>
      </NDataTable>
    </NCard>

    <NCard size="small" :title="t('acl.f2bTitle')" class="gap">
      <template #header-extra>
        <NText depth="3" class="small">{{ bans ? t('acl.count', { n: bans.length }) : '' }}</NText>
      </template>
      <template v-if="jails.length">
        <div class="form">
          <NSelect v-model:value="banForm.jail" :options="jailOptions" class="dur" />
          <NInput v-model:value="banForm.ip" :placeholder="t('acl.singleIp')" class="ip" @keyup.enter="banForm.ip.trim() && banAction('ban', banForm.jail, banForm.ip.trim())" />
          <NButton type="error" :disabled="!banForm.ip.trim()" :loading="busy" @click="banAction('ban', banForm.jail, banForm.ip.trim())">
            {{ t('acl.ban') }}
          </NButton>
        </div>
        <NText depth="3" class="hint">{{ t('acl.f2bHint') }}</NText>
        <div class="form">
          <NSelect v-model:value="banJail" :options="jailFilterOptions" class="dur" :placeholder="t('acl.allJails')" clearable />
          <NInput v-model:value="banFilter" :placeholder="t('acl.searchIp')" class="ip" clearable />
        </div>
        <MobileCards v-if="isMobile" :columns="banColumns" :data="shownBans" :row-key="(b: F2BBan) => b.jail + b.ip" title-key="ip" actions-key="actions" :empty="t('acl.noBans')" />
        <NDataTable
          v-else
          :columns="banColumns"
          :data="shownBans"
          :loading="bans === null"
          :bordered="false"
          size="small"
          :max-height="420"
          virtual-scroll
          :row-key="(b: F2BBan) => b.jail + b.ip"
        >
          <template #empty><NEmpty :description="t('acl.noBans')" /></template>
        </NDataTable>
      </template>
      <NText v-else depth="3">{{ t('acl.noF2B') }}</NText>
    </NCard>
  </template>
</template>

<style scoped>
.gap {
  margin-bottom: 16px;
}
.gap-s {
  margin-bottom: 12px;
}
.top {
  margin-top: 10px;
}
.small {
  font-size: 12px;
}
.hint {
  display: block;
  font-size: 12px;
  margin: 6px 0 12px;
}
.form {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
.form .ip {
  flex: 1 1 200px;
}
.form .note {
  flex: 1 1 160px;
}
.form .dur {
  width: 130px;
}
.confirm-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  align-items: center;
  justify-content: space-between;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
