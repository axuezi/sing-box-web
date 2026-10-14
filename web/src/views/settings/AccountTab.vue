<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NSpace, useMessage } from 'naive-ui'
import { ApiError, post, put } from '../../api'
import { forgetSession, me } from '../../composables/session'
import { formatDateTime } from '../../composables/format'
import InfoRow from '../../components/InfoRow.vue'
import TotpSettings from './TotpSettings.vue'

const MIN_LEN = 10
const { t } = useI18n()
const router = useRouter()

const message = useMessage()

// --- login name ---
const account = reactive({ username: me.value?.username ?? '', current: '' })
const accountBusy = ref(false)
const accountError = ref('')
const nameOk = () => /^[A-Za-z0-9][A-Za-z0-9._@-]{2,31}$/.test(account.username.trim())

async function saveAccount() {
  accountError.value = ''
  if (!nameOk()) return (accountError.value = t('settings.usernameRule'))
  if (!account.current) return (accountError.value = t('settings.needCurrent'))
  accountBusy.value = true
  try {
    const res = await put<{ username: string }>('/account', { username: account.username.trim(), current: account.current })
    if (me.value) me.value.username = res.username
    account.username = res.username
    account.current = ''
    message.success(t('settings.usernameSaved'))
  } catch (e) {
    accountError.value =
      e instanceof ApiError && e.code === 'bad_credentials'
        ? t('settings.wrongCurrent')
        : t('common.requestFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    accountBusy.value = false
  }
}

// --- password ---
const form = reactive({ current: '', next: '', repeat: '' })
const busy = ref(false)
const error = ref('')

async function submit() {
  error.value = ''
  if (form.next.length < MIN_LEN) return (error.value = t('settings.tooShort', { n: MIN_LEN }))
  if (form.next !== form.repeat) return (error.value = t('settings.mismatch'))
  busy.value = true
  try {
    await post('/password', { current: form.current, new: form.next })
    forgetSession()
    router.replace({ name: 'login', query: { changed: '1' } })
  } catch (e) {
    error.value =
      e instanceof ApiError && e.code === 'bad_credentials'
        ? t('settings.wrongCurrent')
        : t('common.requestFailed', { msg: e instanceof Error ? e.message : String(e) })
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NSpace vertical :size="16" class="settings">
    <NCard :title="t('settings.account')" size="small">
      <NForm label-placement="top" @submit.prevent="saveAccount">
        <NFormItem :label="t('login.username')">
          <div class="field">
            <NInput v-model:value="account.username" maxlength="32" :input-props="{ autocomplete: 'username' }" />
            <span class="hint">{{ t('settings.usernameHint') }}</span>
          </div>
        </NFormItem>
        <NFormItem :label="t('settings.current')">
          <NInput v-model:value="account.current" type="password" placeholder="" show-password-on="click"
            :input-props="{ autocomplete: 'current-password' }" @keyup.enter="saveAccount" />
        </NFormItem>
        <NAlert v-if="accountError" type="error" :show-icon="false" class="err">{{ accountError }}</NAlert>
        <NButton type="primary" :loading="accountBusy" :disabled="account.username.trim() === me?.username" attr-type="submit"
          @click="saveAccount">{{ t('settings.saveUsername') }}</NButton>
      </NForm>
    </NCard>
    <NCard :title="t('settings.password')" size="small">
      <NForm label-placement="top" @submit.prevent="submit">
        <input type="text" autocomplete="username" :value="me?.username" hidden />
        <NFormItem :label="t('settings.current')">
          <NInput v-model:value="form.current" type="password" placeholder="" show-password-on="click"
            :input-props="{ autocomplete: 'current-password' }" />
        </NFormItem>
        <NFormItem :label="t('settings.new')">
          <NInput v-model:value="form.next" type="password" placeholder="" show-password-on="click"
            :input-props="{ autocomplete: 'new-password' }" />
        </NFormItem>
        <NFormItem :label="t('settings.repeat')">
          <NInput v-model:value="form.repeat" type="password" placeholder="" show-password-on="click"
            :input-props="{ autocomplete: 'new-password' }" @keyup.enter="submit" />
        </NFormItem>
        <NAlert v-if="error" type="error" :show-icon="false" class="err">{{ error }}</NAlert>
        <NButton type="primary" :loading="busy" attr-type="submit" @click="submit">{{ t('settings.submit') }}</NButton>
      </NForm>
    </NCard>
    <TotpSettings />
    <NCard :title="t('settings.session')" size="small">
      <InfoRow :label="t('login.username')">{{ me?.username }}</InfoRow>
      <InfoRow :label="t('settings.expires')">{{ formatDateTime(me?.expires_at) }}</InfoRow>
    </NCard>
  </NSpace>
</template>

<style scoped>
.settings {
  max-width: 760px;
}
.field {
  width: 100%;
}
.hint {
  display: block;
  font-size: 12px;
  margin-top: 4px;
  opacity: 0.6;
}
.err {
  margin-bottom: 16px;
}
</style>
