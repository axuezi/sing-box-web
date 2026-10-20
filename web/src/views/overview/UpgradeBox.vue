<script setup lang="ts">
// New sing-box-web releases and one-click upgrades (panel, and the sing-box
// build attached to a release when install.sh installed sing-box). Without
// the host's upgrade unit (e.g. Docker) new versions are only shown.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NSpin, NText, useDialog, useMessage } from 'naive-ui'
import { ArrowUpCircle } from 'lucide-vue-next'
import { api, ApiError, post, type UpgradeInfo, type UpgradeState, type UpgradeTarget } from '../../api'
import { formatDateTime } from '../../composables/format'

const { t, te } = useI18n()
const dialog = useDialog()
const message = useMessage()

const info = ref<UpgradeInfo | null>(null)
const starting = ref(false)
// Set while our panel upgrade runs: the panel restarts, so requests fail
// for a while, and once the new version answers the page must reload to
// pick up its frontend.
const waitingPanel = ref(false)

const state = computed(() => info.value?.state)
const busy = computed(() => state.value?.status === 'requested' || state.value?.status === 'running')
const latest = computed(() => info.value?.panel.latest)
const panelNewer = computed(() => !!info.value?.panel.newer && !!latest.value)
const kernelNewer = computed(() => !!info.value?.sing_box.managed && !!info.value?.sing_box.newer)

// A finished upgrade stays on show for a day.
const recent = computed<UpgradeState | undefined>(() => {
  const s = state.value
  if (!s || busy.value) return undefined
  const at = Date.parse(s.finished ?? s.started ?? '')
  return Number.isFinite(at) && Date.now() - at < 24 * 3600_000 ? s : undefined
})

const what = (s: UpgradeState) => (s.target === 'panel' ? t('upgrade.panel') : 'sing-box')
const stage = computed(() => {
  const m = state.value?.message ?? ''
  if (state.value?.status === 'requested') return t('upgrade.stage.requested')
  if (m === 'downloading' || m === 'installing') return t(`upgrade.stage.${m}`)
  if (m.startsWith('checking')) return t('upgrade.stage.checking')
  return m
})
const resultType = (s: UpgradeState) => (s.status === 'done' ? 'success' : s.status === 'rolled_back' ? 'warning' : 'error')

let timer = 0
async function load() {
  try {
    const v = await api<UpgradeInfo>('/upgrade')
    info.value = v
    const s = v.state
    if (s?.target === 'panel' && (s.status === 'requested' || s.status === 'running')) waitingPanel.value = true
    if (waitingPanel.value && s && s.status !== 'requested' && s.status !== 'running') {
      waitingPanel.value = false
      if (s.status === 'done') {
        message.success(t('upgrade.reloading', { v: s.to ?? '' }))
        setTimeout(() => location.reload(), 1500)
      }
    }
  } catch {
    // The panel is restarting during its own upgrade; keep asking.
  }
  schedule()
}
function schedule() {
  clearTimeout(timer)
  timer = window.setTimeout(load, busy.value || waitingPanel.value ? 2000 : 30_000)
}
onMounted(load)
onBeforeUnmount(() => clearTimeout(timer))

function start(target: UpgradeTarget) {
  const v = info.value
  if (!v) return
  const panel = target === 'panel'
  dialog.warning({
    title: panel ? t('upgrade.confirmPanelTitle', { v: v.panel.latest?.version ?? '' }) : t('upgrade.confirmKernelTitle', { v: v.sing_box.bundled ?? '' }),
    content: panel ? t('upgrade.confirmPanel') : t('upgrade.confirmKernel'),
    positiveText: t('upgrade.upgrade'),
    negativeText: t('common.cancel'),
    onPositiveClick: async () => {
      starting.value = true
      try {
        const r = await post<{ state: UpgradeState }>('/upgrade', { target })
        if (info.value) info.value.state = r.state
        if (panel) waitingPanel.value = true
      } catch (e) {
        const code = e instanceof ApiError ? e.code : ''
        message.error(te(`upgrade.errors.${code}`) ? t(`upgrade.errors.${code}`) : e instanceof Error ? e.message : String(e), { duration: 8000 })
      } finally {
        starting.value = false
        schedule()
      }
    },
  })
}
</script>

<template>
  <div v-if="info && (panelNewer || kernelNewer || busy || recent)" class="upg">
    <div v-if="busy && state" class="line">
      <NSpin :size="14" />
      <NText>{{ t('upgrade.busy', { what: what(state), v: state.to || '…' }) }}</NText>
      <NText depth="3" class="stage">{{ stage }}</NText>
    </div>
    <NAlert v-else-if="recent" :type="resultType(recent)" :show-icon="false" class="result">
      <div>{{ t(`upgrade.result.${recent.status}`, { what: what(recent), from: recent.from || '—', to: recent.to || '—' }) }}</div>
      <NText v-if="recent.message && recent.status !== 'done'" depth="3" class="msg">{{ recent.message }}</NText>
      <NText v-if="recent.finished" depth="3" class="msg">{{ formatDateTime(recent.finished) }}</NText>
    </NAlert>

    <div v-if="panelNewer && latest" class="line">
      <ArrowUpCircle :size="15" class="icon" />
      <NText>{{ t('upgrade.panelAvailable', { v: latest.version }) }}</NText>
      <a :href="latest.url" target="_blank" rel="noopener noreferrer" class="link">{{ t('upgrade.notes') }}</a>
      <NButton v-if="info.enabled" size="tiny" type="primary" secondary class="btn" :loading="starting" :disabled="busy" @click="start('panel')">
        {{ t('upgrade.upgrade') }}
      </NButton>
    </div>
    <NText v-if="panelNewer && !info.enabled" depth="3" class="note">{{ t('upgrade.manualHint') }}</NText>

    <div v-if="kernelNewer" class="line">
      <ArrowUpCircle :size="15" class="icon" />
      <NText>{{ t('upgrade.kernelAvailable', { v: info.sing_box.bundled }) }}</NText>
      <NButton size="tiny" type="primary" secondary class="btn" :loading="starting" :disabled="busy" @click="start('sing-box')">
        {{ t('upgrade.upgrade') }}
      </NButton>
    </div>
  </div>
</template>

<style scoped>
.upg {
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 10px;
  font-size: 13px;
}
.line {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px 8px;
  min-width: 0;
}
.icon {
  color: #18a058;
  flex-shrink: 0;
}
.stage {
  overflow-wrap: anywhere;
}
.link {
  color: #18a058;
  font-size: 12px;
  text-decoration: none;
}
.btn {
  margin-left: auto;
}
.result :deep(.n-alert-body) {
  padding: 8px 12px;
}
.msg {
  display: block;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.note {
  font-size: 12px;
  line-height: 1.5;
}
</style>
