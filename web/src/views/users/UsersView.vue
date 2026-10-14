<script setup lang="ts">
import { computed, h, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCard, NCheckbox, NDataTable, NDropdown, NEmpty, NProgress, NRadioButton, NRadioGroup, NSpace, NSwitch,
  NTag, NText, NTooltip, useDialog,
} from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, del, patch, post, type ApplyResult, type User, type UserInput, type UsersResponse } from '../../api'
import { formatAgo, formatBytes, formatDate, formatDateTime } from '../../composables/format'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import UserFormModal from './UserFormModal.vue'
import ClientDrawer from './ClientDrawer.vue'
import MobileCards from '../../components/MobileCards'
import { useIsMobile } from '../../composables/viewport'

const { t } = useI18n()
const isMobile = useIsMobile()
const dialog = useDialog()
const feedback = useApplyFeedback()

const data = ref<UsersResponse | null>(null)
const busy = ref(false)
const toggling = ref<number | null>(null)
const formOpen = ref(false)
const editing = ref<User | null>(null)
const drawerOpen = ref(false)
const drawerUser = ref<User | null>(null)

async function load() {
  try {
    data.value = await api<UsersResponse>('/users')
  } catch (e) {
    if (!isNetworkError(e)) feedback.failure(e)
  }
}
onMounted(load)

// Online status changes by itself: refresh while the page is visible.
const timer = setInterval(() => {
  if (!busy.value && document.visibilityState === 'visible') load()
}, 15_000)
onBeforeUnmount(() => clearInterval(timer))

const presence = computed(() => data.value?.presence)
const onlineCount = computed(() => data.value?.users.filter((u) => u.online === 'online').length ?? 0)

type Res = { result: ApplyResult; users: User[] }

async function run<T extends Res>(fn: () => Promise<T>) {
  busy.value = true
  try {
    const res = await fn()
    feedback.success(res.result)
    await load()
    return res
  } catch (e) {
    if (isNetworkError(e)) formOpen.value = false
    feedback.failure(e, load)
    await load()
  } finally {
    busy.value = false
  }
}

async function submitForm(v: UserInput) {
  const { enabled, ...create } = v
  const res = editing.value
    ? await run(() => patch<Res>(`/users/${editing.value!.id}`, v))
    : await run(() => post<Res>('/users', create))
  if (res) {
    formOpen.value = false
    if (!editing.value && res.users[0]) openClient(res.users[0])
  }
}

async function toggle(u: User, enabled: boolean) {
  toggling.value = u.id
  await run(() => patch<Res>(`/users/${u.id}`, { enabled }))
  toggling.value = null
}

function openClient(u: User) {
  drawerUser.value = u
  drawerOpen.value = true
}

function confirmDelete(u: User) {
  dialog.warning({
    title: t('users.delete'),
    content: t('users.deleteConfirm', { name: u.name }),
    positiveText: t('users.delete'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => run(() => del<Res>(`/users/${u.id}`)),
  })
}

function confirmReset(u: User) {
  const pick = reactive({ password: false, uuid: false, token: true })
  dialog.warning({
    title: t('users.resetTitle', { name: u.name }),
    content: () =>
      h('div', [
        h(NSpace, { vertical: true, size: 6 }, () => [
          h(NCheckbox, { checked: pick.token, 'onUpdate:checked': (v: boolean) => (pick.token = v) }, () => t('users.resetToken')),
          h(NCheckbox, { checked: pick.password, 'onUpdate:checked': (v: boolean) => (pick.password = v) }, () => t('users.resetPassword')),
          h(NCheckbox, { checked: pick.uuid, 'onUpdate:checked': (v: boolean) => (pick.uuid = v) }, () => t('users.resetUuid')),
        ]),
        h(NText, { depth: 3, style: 'display:block;margin-top:12px;font-size:12px' }, () => t('users.resetHint')),
      ]),
    positiveText: t('users.reset'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => {
      if (!pick.password && !pick.uuid && !pick.token) return false
      return run(() => post<Res>(`/users/${u.id}/reset`, { ...pick })).then(() => true)
    },
  })
}

function confirmRenew(u: User) {
  const pick = reactive({ months: 1, resetUsage: u.limited === 'quota' })
  const newExpiry = () => {
    const now = Date.now()
    const cur = u.expires_at ? new Date(u.expires_at).getTime() : 0
    const d = new Date(Math.max(now, cur))
    d.setMonth(d.getMonth() + pick.months)
    return formatDateTime(d.toISOString())
  }
  dialog.info({
    title: t('users.renewTitle', { name: u.name }),
    content: () =>
      h('div', [
        h(NRadioGroup, { value: pick.months, 'onUpdate:value': (v: number) => (pick.months = v), size: 'small' }, () =>
          [1, 3, 6, 12].map((n) =>
            h(NRadioButton, { value: n }, () => (n === 12 ? t('users.renewYear') : t('users.renewMonths', { n }, n))))),
        h(NText, { style: 'display:block;margin-top:12px' }, () => t('users.newExpiry', { date: newExpiry() })),
        u.quota_bytes > 0
          ? h(NCheckbox, {
              checked: pick.resetUsage,
              style: 'margin-top:12px',
              'onUpdate:checked': (v: boolean) => (pick.resetUsage = v),
            }, () => t('users.renewResetUsage'))
          : null,
        h(NText, { depth: 3, style: 'display:block;margin-top:12px;font-size:12px' }, () => t('users.renewHint')),
      ]),
    positiveText: t('users.renew'),
    negativeText: t('common.cancel'),
    onPositiveClick: () =>
      run(() => post<Res>(`/users/${u.id}/renew`, { months: pick.months, reset_usage: pick.resetUsage })).then(() => true),
  })
}

function confirmResetUsage(u: User) {
  dialog.warning({
    title: t('users.resetUsageTitle', { name: u.name }),
    content: t('users.resetUsageConfirm', { used: formatBytes(u.cycle_up + u.cycle_down) }),
    positiveText: t('users.resetUsage'),
    negativeText: t('common.cancel'),
    onPositiveClick: () => run(() => post<Res>(`/users/${u.id}/reset-usage`, {})).then(() => true),
  })
}

const moreOptions = computed(() => [
  { key: 'edit', label: t('users.edit') },
  { key: 'renew', label: t('users.renew') },
  { key: 'resetUsage', label: t('users.resetUsage') },
  { key: 'reset', label: t('users.reset') },
  { type: 'divider', key: 'd' },
  { key: 'delete', label: () => h(NText, { type: 'error' }, () => t('users.delete')) },
])

function onMore(key: string, u: User) {
  if (key === 'edit') {
    editing.value = u
    formOpen.value = true
  } else if (key === 'renew') confirmRenew(u)
  else if (key === 'resetUsage') confirmResetUsage(u)
  else if (key === 'reset') confirmReset(u)
  else if (key === 'delete') confirmDelete(u)
}

const presenceRank = (u: User) =>
  (u.online === 'online' ? 2e12 : u.online === 'unknown' ? 1e12 : 0) + (u.last_active_at ? new Date(u.last_active_at).getTime() / 1000 : 0)

/** Usage shown in the list: the quota cycle when there is a quota, else this month. */
const usedOf = (u: User) => (u.quota_bytes > 0 ? u.cycle_up + u.cycle_down : u.month_up + u.month_down)
const expiryMs = (u: User) => (u.expires_at ? new Date(u.expires_at).getTime() : Number.MAX_SAFE_INTEGER)

const columns = computed<DataTableColumns<User>>(() => [
  {
    title: t('users.name'),
    key: 'name',
    minWidth: 160,
    render: (u) =>
      h('div', [
        h(NSpace, { size: 6, align: 'center', wrap: false }, () => [
          h(NText, { strong: true }, () => u.name),
          u.limited
            ? h(NTooltip, null, {
                trigger: () =>
                  h(NTag, { size: 'tiny', bordered: false, type: u.limited === 'expired' ? 'error' : 'warning' }, () =>
                    u.limited === 'expired' ? t('users.expired') : t('users.overQuota')),
                default: () => t('users.limitedHint'),
              })
            : null,
        ]),
        u.note ? h(NText, { depth: 3, style: 'display:block;font-size:12px' }, () => u.note) : null,
      ]),
  },
  {
    title: t('users.inbounds'),
    key: 'inbounds',
    minWidth: 150,
    render: (u) =>
      u.inbounds.length
        ? h(NSpace, { size: 4 }, () => u.inbounds.map((tag) => h(NTag, { size: 'small', bordered: false }, () => tag)))
        : h(NText, { type: 'warning' }, () => '—'),
  },
  {
    title: t('users.enabled'),
    key: 'enabled',
    width: 84,
    render: (u) =>
      h(NSwitch, {
        value: u.enabled,
        size: 'small',
        loading: toggling.value === u.id,
        disabled: busy.value,
        'onUpdate:value': (v: boolean) => toggle(u, v),
      }),
  },
  {
    title: t('users.traffic'),
    key: 'traffic',
    width: 160,
    sorter: (a, b) => usedOf(a) - usedOf(b),
    render: (u) => {
      const used = usedOf(u)
      if (u.quota_bytes > 0) {
        const pct = Math.min(100, (used / u.quota_bytes) * 100)
        return h('div', { class: 'quota' }, [
          h('span', { class: 'num' }, t('users.usedOf', { used: formatBytes(used), quota: formatBytes(u.quota_bytes) })),
          h(NProgress, {
            type: 'line',
            percentage: pct,
            showIndicator: false,
            height: 4,
            status: pct >= 100 ? 'error' : pct >= 80 ? 'warning' : 'default',
          }),
          h(NText, { depth: 3, style: 'font-size:12px' }, () => t('users.nextReset', { date: formatDate(u.next_reset) })),
        ])
      }
      return used > 0 ? h('span', { class: 'num' }, formatBytes(used)) : h(NText, { depth: 3 }, () => '—')
    },
  },
  {
    title: t('users.expires'),
    key: 'expires_at',
    width: 110,
    sorter: (a, b) => expiryMs(a) - expiryMs(b),
    render: (u) => {
      if (!u.expires_at) return h(NText, { depth: 3 }, () => t('users.neverExpires'))
      const left = new Date(u.expires_at).getTime() - Date.now()
      const days = Math.floor(left / 86_400_000)
      const hint = left <= 0 ? t('users.expired') : days < 1 ? t('users.hoursLeft') : t('users.daysLeft', { n: days })
      const type = left <= 0 ? 'error' : days < 7 ? 'warning' : undefined
      return h('div', [
        h('span', { class: 'num' }, formatDate(u.expires_at)),
        h(NText, { type, depth: type ? undefined : 3, style: 'display:block;font-size:12px' }, () => hint),
      ])
    },
  },
  {
    title: t('users.presence'),
    key: 'online',
    width: 160,
    sorter: (a, b) => presenceRank(a) - presenceRank(b),
    render: (u) => {
      const label =
        u.online === 'online'
          ? `${t('users.online')} · ${t('users.connsN', { n: u.conns }, u.conns)}`
          : u.online === 'offline'
            ? t('users.offline')
            : t('users.unknownState')
      const state = h('span', { class: ['presence', u.online] }, [h('i', { class: 'dot' }), label])
      return h('div', [
        u.online === 'unknown'
          ? h(NTooltip, null, {
              trigger: () => state,
              default: () =>
                presence.value?.available === false
                  ? t('users.presenceDown', { error: presence.value.error ?? '' })
                  : t('users.unknownHint'),
            })
          : state,
        h(NText, { depth: 3, style: 'display:block;font-size:12px' }, () =>
          u.last_active_at ? t('users.lastActive', { ago: formatAgo(u.last_active_at, t) }) : t('users.neverActive')),
      ])
    },
  },
  {
    title: t('users.actions'),
    key: 'actions',
    width: 150,
    fixed: 'right',
    render: (u) =>
      h(NSpace, { size: 6, wrap: false }, () => [
        h(NButton, { size: 'small', type: 'primary', ghost: true, onClick: () => openClient(u) }, () => t('users.connect')),
        h(
          NDropdown,
          { options: moreOptions.value, trigger: 'click', onSelect: (k: string) => onMore(k, u) },
          () => h(NButton, { size: 'small', quaternary: true, disabled: busy.value }, () => '⋯'),
        ),
      ]),
  },
])
</script>

<template>
  <NCard size="small">
    <template #header>
      <NSpace v-if="data" :size="12" align="center">
        <span>{{ t('users.count', { n: data.users.length }) }}</span>
        <NText v-if="presence?.available" depth="3" class="sub">
          <i class="dot" :class="{ on: onlineCount }" />{{ t('users.onlineCount', { n: onlineCount }) }}
        </NText>
        <NTooltip v-if="presence?.available && presence.unattributed > 0">
          <template #trigger>
            <NText depth="3" class="sub hint">{{ t('users.unattributed', { n: presence.unattributed }, presence.unattributed) }}</NText>
          </template>
          {{ t('users.unattributedHint') }}
        </NTooltip>
        <NText v-if="presence && !presence.available" type="warning" class="sub">
          {{ t('users.presenceDown', { error: presence.error ?? '' }) }}
        </NText>
      </NSpace>
    </template>
    <template #header-extra>
      <NButton
        type="primary"
        size="small"
        :disabled="busy || !data"
        @click="
          editing = null;
          formOpen = true
        "
      >
        + {{ t('users.add') }}
      </NButton>
    </template>
    <MobileCards
      v-if="isMobile && data"
      :columns="columns"
      :data="data.users"
      :row-key="(u: User) => u.id"
      title-key="name"
      actions-key="actions"
      :empty="t('users.empty')"
    />
    <NDataTable
      v-else
      :columns="columns"
      :data="data?.users ?? []"
      :loading="!data"
      :bordered="false"
      size="small"
      :scroll-x="970"
      :row-key="(u: User) => u.id"
    >
      <template #empty><NEmpty :description="t('users.empty')" /></template>
    </NDataTable>
  </NCard>

  <UserFormModal
    v-model:show="formOpen"
    :user="editing"
    :inbounds="data?.inbounds ?? []"
    :busy="busy"
    @submit="submitForm"
  />
  <ClientDrawer v-model:show="drawerOpen" :user="drawerUser" />
</template>

<style scoped>
.sub {
  font-size: 12px;
  font-weight: normal;
}
.hint {
  border-bottom: 1px dashed currentColor;
  cursor: help;
}
:deep(.presence) {
  white-space: nowrap;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-variant-numeric: tabular-nums;
}
:deep(.presence.unknown) {
  cursor: help;
}
:deep(.dot) {
  display: inline-block;
  width: 7px;
  height: 7px;
  border-radius: 50%;
  margin-right: 6px;
  background: var(--n-text-color-3, #999);
  opacity: 0.6;
}
:deep(.presence .dot) {
  margin-right: 0;
}
:deep(.presence.online .dot),
.dot.on {
  background: #18a058;
  opacity: 1;
}
:deep(.presence.unknown .dot) {
  background: transparent;
  border: 1.5px dashed #999;
  width: 6px;
  height: 6px;
}
:deep(.num) {
  font-variant-numeric: tabular-nums;
}
:deep(.quota) {
  display: flex;
  flex-direction: column;
  gap: 3px;
}
</style>
