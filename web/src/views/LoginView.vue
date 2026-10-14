<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCard, NForm, NFormItem, NInput, NText } from 'naive-ui'
import { ApiError } from '../api'
import { login, setupRequired } from '../composables/session'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const form = reactive({ username: '', password: '', code: '' })
const needCode = ref(false)
const busy = ref(false)
const error = ref('')

async function submit() {
  if (!form.username || !form.password) return
  busy.value = true
  error.value = ''
  try {
    await login(form.username, form.password, form.code)
    const next = typeof route.query.next === 'string' && route.query.next.startsWith('/') ? route.query.next : '/'
    router.replace(next)
  } catch (e) {
    if (e instanceof ApiError && e.code === 'totp_required') {
      needCode.value = true
      error.value = t('login.totpRequired')
      return
    }
    if (e instanceof ApiError && e.code === 'locked') {
      error.value = t('login.locked', { min: 15 })
    } else if (e instanceof ApiError && e.code === 'bad_credentials') {
      error.value = t('login.badCredentials')
    } else if (e instanceof ApiError && e.code === 'setup_required') {
      setupRequired.value = true
    } else {
      error.value = t('common.requestFailed', { msg: e instanceof Error ? e.message : String(e) })
    }
    if (!needCode.value) form.password = ''
    form.code = ''
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="page">
    <NCard class="card" :bordered="true">
      <div class="brand">
        <img src="/favicon.svg" alt="" width="40" height="40" />
        <div>
          <div class="name">sing-box-web</div>
          <NText depth="3">{{ t('login.subtitle') }}</NText>
        </div>
      </div>

      <NAlert v-if="setupRequired" type="warning" :title="t('login.setupTitle')" class="block">
        {{ t('login.setupHint') }}
        <pre class="cmd">sing-box-web passwd -data-dir &lt;DATA_DIR&gt;</pre>
      </NAlert>

      <NForm v-else :show-label="true" label-placement="top" @submit.prevent="submit">
        <NFormItem :label="t('login.username')">
          <NInput v-model:value="form.username" placeholder="" autofocus :input-props="{ autocomplete: 'username' }" />
        </NFormItem>
        <NFormItem :label="t('login.password')">
          <NInput
            v-model:value="form.password"
            placeholder=""
            type="password"
            show-password-on="click"
            :input-props="{ autocomplete: 'current-password' }"
            @keyup.enter="submit"
          />
        </NFormItem>
        <NFormItem v-if="needCode" :label="t('login.code')">
          <NInput
            v-model:value="form.code"
            placeholder=""
            maxlength="6"
            autofocus
            :input-props="{ autocomplete: 'one-time-code', inputmode: 'numeric' }"
            @keyup.enter="submit"
          />
        </NFormItem>
        <NAlert v-if="error" :type="needCode && !form.code ? 'info' : 'error'" :show-icon="false" class="block">{{ error }}</NAlert>
        <NButton type="primary" block :loading="busy" attr-type="submit" @click="submit">
          {{ t('login.submit') }}
        </NButton>
      </NForm>
    </NCard>
  </div>
</template>

<style scoped>
.page {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  box-sizing: border-box;
}
.card {
  width: 100%;
  max-width: 380px;
}
.brand {
  display: flex;
  align-items: center;
  gap: 14px;
  margin-bottom: 24px;
}
.name {
  font-size: 20px;
  font-weight: 600;
}
.block {
  margin-bottom: 16px;
}
.cmd {
  margin: 8px 0 0;
  padding: 8px 10px;
  border-radius: 4px;
  background: rgba(127, 127, 127, 0.12);
  font-family: var(--n-font-family-mono, monospace);
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
