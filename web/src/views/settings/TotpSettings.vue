<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCard, NInput, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { api, post } from '../../api'
import QrCode from '../../components/QrCode.vue'

const { t } = useI18n()
const message = useMessage()
const enabled = ref<boolean | null>(null)
const setup = ref<{ secret: string; uri: string } | null>(null)
const disabling = ref(false)
const code = ref('')
const password = ref('')
const busy = ref(false)

async function load() {
  enabled.value = (await api<{ enabled: boolean }>('/totp')).enabled
}
onMounted(load)

async function start() {
  setup.value = await post('/totp/setup')
  code.value = ''
}
async function confirm() {
  busy.value = true
  try {
    await post('/totp/enable', { code: code.value })
    message.success(t('totp.enabledMsg'))
    setup.value = null
    await load()
  } catch {
    message.error(t('totp.badCode'))
  } finally {
    busy.value = false
  }
}
async function disable() {
  busy.value = true
  try {
    await post('/totp/disable', { code: code.value, password: password.value })
    message.success(t('totp.disabledMsg'))
    disabling.value = false
    password.value = code.value = ''
    await load()
  } catch {
    message.error(t('settings.wrongCurrent') + ' / ' + t('totp.badCode'))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NCard :title="t('totp.title')" size="small">
    <template #header-extra>
      <NTag v-if="enabled !== null" size="small" :type="enabled ? 'success' : 'default'" :bordered="false">
        {{ enabled ? t('totp.enabled') : t('totp.disabled') }}
      </NTag>
    </template>
    <template v-if="enabled === false">
      <NButton v-if="!setup" size="small" type="primary" @click="start">{{ t('totp.enable') }}</NButton>
      <div v-else class="setup">
        <QrCode :text="setup.uri" :size="180" />
        <div class="steps">
          <NText depth="3">{{ t('totp.scan') }}</NText>
          <code class="secret">{{ setup.secret.replace(/(.{4})/g, '$1 ').trim() }}</code>
          <NSpace :size="8">
            <NInput v-model:value="code" :placeholder="t('totp.code')" maxlength="6" class="code" @keyup.enter="confirm" />
            <NButton type="primary" :loading="busy" :disabled="code.length !== 6" @click="confirm">{{ t('totp.confirm') }}</NButton>
          </NSpace>
        </div>
      </div>
    </template>
    <template v-else-if="enabled">
      <NButton v-if="!disabling" size="small" @click="disabling = true">{{ t('totp.disable') }}</NButton>
      <NSpace v-else vertical :size="8">
        <NText depth="3">{{ t('totp.disableHint') }}</NText>
        <NInput v-model:value="password" type="password" :placeholder="t('settings.current')" />
        <NSpace :size="8">
          <NInput v-model:value="code" :placeholder="t('totp.code')" maxlength="6" class="code" />
          <NButton type="error" :loading="busy" @click="disable">{{ t('totp.disable') }}</NButton>
          <NButton @click="disabling = false">{{ t('common.cancel') }}</NButton>
        </NSpace>
      </NSpace>
    </template>
  </NCard>
</template>

<style scoped>
.setup {
  display: flex;
  gap: 20px;
  flex-wrap: wrap;
  align-items: flex-start;
}
.steps {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
  min-width: 220px;
}
.secret {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
  word-break: break-all;
  padding: 6px 8px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.12);
}
.code {
  width: 140px;
}
</style>
