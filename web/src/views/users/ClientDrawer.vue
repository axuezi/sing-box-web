<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import {
  NAlert, NButton, NDrawer, NDrawerContent, NInput, NInputGroup, NSpin, NTabPane, NTabs, NTag, NText, useMessage,
} from 'naive-ui'
import { api, type ClientInfo, type SubAccess, type TrafficStats, type User } from '../../api'
import { formatDateTime } from '../../composables/format'
import DailyBars from '../../components/charts/DailyBars.vue'
import { copyText } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'
import QrCode from '../../components/QrCode.vue'

const props = defineProps<{ show: boolean; user: User | null }>()
const emit = defineEmits<{ 'update:show': [boolean] }>()
const { t } = useI18n()
const router = useRouter()
const message = useMessage()
const isMobile = useIsMobile()

const info = ref<ClientInfo | null>(null)
const access = ref<SubAccess[]>([])
const days = ref<{ day: string; up: number; down: number }[]>([])
const error = ref('')
const qrFor = ref('')
const qrName = ref('') // which link the QR code holds

watch(
  () => [props.show, props.user?.id],
  async () => {
    if (!props.show || !props.user) return
    info.value = null
    error.value = ''
    access.value = []
    days.value = []
    const id = props.user.id
    api<{ access: SubAccess[] }>(`/users/${id}/access`).then((r) => (access.value = r.access)).catch(() => {})
    api<TrafficStats>('/stats/traffic?days=30')
      .then((r) => (days.value = r.series.find((s) => s.kind === 'user' && s.key === String(id))?.daily ?? []))
      .catch(() => {})
    try {
      info.value = await api<ClientInfo>(`/users/${id}/client`)
      qrFor.value = info.value.subscription.auto
      qrName.value = t('users.subAuto')
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
    }
  },
  { immediate: true },
)

const subs = computed(() =>
  info.value
    ? [
        { key: 'auto', label: t('users.subAuto'), url: info.value.subscription.auto },
        { key: 'mihomo', label: 'Mihomo / Clash Verge', url: info.value.subscription.mihomo },
        { key: 'singbox', label: 'sing-box (SFA / SFI / SFM)', url: info.value.subscription.singbox },
        { key: 'base64', label: 'v2rayN / Shadowrocket (base64)', url: info.value.subscription.base64 },
      ]
    : [],
)
const skipped = computed(() => Object.entries(info.value?.skipped ?? {}))

async function copy(text: string) {
  await copyText(text)
  message.success(t('common.copied'))
}
</script>

<template>
  <NDrawer :show="show" :width="isMobile ? '100%' : 520" placement="right" @update:show="emit('update:show', $event)">
    <NDrawerContent :title="user ? `${t('users.connect')} · ${user.name}` : ''" closable>
      <NSpin :show="!info && !error">
        <NAlert v-if="error" type="error">{{ error }}</NAlert>
        <template v-if="info">
          <NAlert v-if="!info.enabled" type="warning" class="gap">{{ t('users.disabledNote') }}</NAlert>
          <NAlert v-else-if="user?.limited" type="warning" class="gap">
            {{ user.limited === 'expired' ? t('users.expiredNote') : t('users.quotaNote') }}
          </NAlert>
          <NAlert v-if="skipped.length" type="warning" :title="t('users.skippedTitle')" class="gap">
            <div v-for="[tag, why] in skipped" :key="tag"><b>{{ tag }}</b>: {{ why }}</div>
            <div class="hint">{{ t('users.skippedHint') }}</div>
            <NButton size="tiny" class="hint" @click="router.push({ name: 'nodes' })">{{ t('users.goSettings') }}</NButton>
          </NAlert>

          <div class="qrbox">
            <QrCode :text="qrFor" :size="200" />
            <NText depth="3" class="qrlabel">{{ t('users.qr') }} · {{ qrName }}</NText>
          </div>

          <NTabs type="segment" size="small" animated>
            <NTabPane name="sub" :tab="t('users.subscription')">
              <div v-for="s in subs" :key="s.key" class="row">
                <div class="label">{{ s.label }}</div>
                <NInputGroup>
                  <NInput :value="s.url" readonly size="small" class="mono" />
                  <NButton size="small" :type="qrFor === s.url ? 'primary' : 'default'" @click="(qrFor = s.url), (qrName = s.label)">QR</NButton>
                  <NButton size="small" type="primary" ghost @click="copy(s.url)">{{ t('common.copy') }}</NButton>
                </NInputGroup>
              </div>
            </NTabPane>
            <NTabPane name="links" :tab="t('users.links')">
              <div v-for="l in info.links" :key="l.inbound" class="row">
                <div class="label">{{ l.name }} <NTag size="tiny" :bordered="false">{{ l.inbound }}</NTag></div>
                <NText v-if="l.error" type="error">{{ l.error }}</NText>
                <NInputGroup v-else>
                  <NInput :value="l.url" readonly size="small" class="mono" />
                  <NButton size="small" :type="qrFor === l.url ? 'primary' : 'default'" @click="(qrFor = l.url!), (qrName = l.name)">QR</NButton>
                  <NButton size="small" type="primary" ghost @click="copy(l.url!)">{{ t('common.copy') }}</NButton>
                </NInputGroup>
              </div>
            </NTabPane>
            <NTabPane name="traffic" :tab="t('access.traffic')">
              <DailyBars v-if="days.length" :days="days" :labels="{ down: t('live.down'), up: t('live.up'), total: t('live.total') }" class="chart" />
              <NText v-else depth="3">{{ t('live.noTraffic') }}</NText>
            </NTabPane>
            <NTabPane name="access" :tab="t('access.title')">
              <NText v-if="!access.length" depth="3">{{ t('access.none') }}</NText>
              <div v-for="(a, i) in access" :key="i" class="acc">
                <div class="acc-top">
                  <span>{{ formatDateTime(a.at) }}</span>
                  <NTag size="tiny" :bordered="false">{{ a.format }}</NTag>
                  <span class="mono">{{ a.ip }}</span>
                </div>
                <NText depth="3" class="ua">{{ a.user_agent || '—' }}</NText>
              </div>
            </NTabPane>
          </NTabs>
        </template>
      </NSpin>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.gap {
  margin-bottom: 12px;
}
.hint {
  margin-top: 6px;
}
.qrbox {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  margin: 4px 0 16px;
}
.qrlabel {
  font-size: 12px;
}
.row {
  margin: 12px 0;
}
.label {
  font-size: 13px;
  margin-bottom: 4px;
}
.chart {
  margin-top: 12px;
}
.acc {
  padding: 8px 0;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
  font-size: 13px;
}
.acc-top {
  display: flex;
  gap: 10px;
  align-items: center;
}
.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
.ua {
  display: block;
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mono :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
