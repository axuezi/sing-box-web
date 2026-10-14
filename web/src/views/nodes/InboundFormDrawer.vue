<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NCheckbox, NDivider, NDrawer, NDrawerContent, NDynamicTags, NForm, NFormItem, NInput, NInputGroup,
  NInputNumber, NRadioButton, NRadioGroup, NSelect, NSpace, NSwitch, NText, useMessage,
} from 'naive-ui'
import { post, put, type ApplyResult, type InboundExport, type InboundForm, type InboundItem } from '../../api'
import { copyText, isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'

const props = defineProps<{ show: boolean; item: InboundItem | null; publicHost: string; userCount: number }>()
const emit = defineEmits<{ 'update:show': [boolean]; saved: [] }>()
const { t } = useI18n()
const message = useMessage()
const feedback = useApplyFeedback()
const isMobile = useIsMobile()

/** Secrets come back masked; sending the mask keeps the stored value. */
const MASK = '••••••••'

function blank(type: InboundForm['type']): InboundForm {
  return {
    tag: '',
    type,
    listen: '::',
    listen_port: type === 'hysteria2' ? 443 : 8443,
    tls: {
      mode: type === 'hysteria2' ? 'tls' : 'reality',
      server_name: '',
      alpn: [],
      cert_source: 'inline',
      certificate_path: '',
      key_path: '',
      certificate: '',
      key: '',
      handshake_server: '',
      handshake_port: 443,
      private_key: '',
      public_key: '',
      short_ids: [],
    },
    up_mbps: 0,
    down_mbps: 0,
    obfs_password: '',
    masquerade: '',
    flow: type === 'vless' ? 'xtls-rprx-vision' : '',
  }
}

const form = reactive<InboundForm>(blank('vless'))
const exp = reactive<InboundExport>({ insecure: false, hidden: false })
const assignUsers = ref(true)
// Secret inputs start empty; empty + a stored secret = keep it.
const keyInput = ref('')
const privInput = ref('')
const generatedPub = ref('')
const busy = ref(false)
const generating = ref('')

const isNew = computed(() => !props.item)
const keyStored = computed(() => props.item?.form.tls.key === MASK)
const privStored = computed(() => props.item?.form.tls.private_key === MASK)

function reset() {
  const src = props.item ? (JSON.parse(JSON.stringify(props.item.form)) as InboundForm) : blank(form.type || 'vless')
  src.tls.alpn ??= []
  src.tls.short_ids ??= []
  src.listen ||= '::'
  Object.assign(form, src)
  Object.assign(exp, { insecure: false, hidden: false, name: undefined, host: undefined, port: undefined }, props.item?.export ?? {})
  keyInput.value = ''
  privInput.value = ''
  generatedPub.value = ''
  assignUsers.value = true
  if (isNew.value && form.type === 'vless') prefillReality()
}

watch(
  () => props.show,
  (open) => {
    if (open) reset()
  },
)

function switchType(type: InboundForm['type']) {
  const tag = form.tag
  Object.assign(form, blank(type), { tag })
  keyInput.value = privInput.value = generatedPub.value = ''
  exp.insecure = false
  if (type === 'vless') prefillReality()
}

async function generate<T>(kind: string, extra: Record<string, string> = {}): Promise<T | undefined> {
  generating.value = kind
  try {
    return await post<T>('/inbounds/generate', { kind, ...extra })
  } catch (e) {
    feedback.failure(e)
  } finally {
    generating.value = ''
  }
}

async function genKeyPair() {
  const r = await generate<{ private_key: string; public_key: string }>('reality_keypair')
  if (r) {
    privInput.value = r.private_key
    generatedPub.value = r.public_key
  }
}

async function genShortId() {
  const r = await generate<{ short_id: string }>('short_id')
  if (r) form.tls.short_ids = [...(form.tls.short_ids ?? []), r.short_id]
}

async function prefillReality() {
  if (!privInput.value) await genKeyPair()
  if (!form.tls.short_ids?.length) await genShortId()
}

async function genSelfSigned() {
  if (!form.tls.server_name.trim()) {
    message.warning(t('inbounds.needServerName'))
    return
  }
  const r = await generate<{ certificate: string; key: string }>('self_signed', { server_name: form.tls.server_name.trim() })
  if (r) {
    form.tls.cert_source = 'inline'
    form.tls.certificate = r.certificate
    keyInput.value = r.key
    exp.insecure = true
    message.info(t('inbounds.selfSignedHint'), { duration: 6000 })
  }
}

async function genObfs() {
  const r = await generate<{ password: string }>('password')
  if (r) form.obfs_password = r.password
}

const publicKey = computed(() => {
  if (generatedPub.value && privInput.value) return generatedPub.value
  if (!privInput.value && privStored.value) return props.item?.form.tls.public_key ?? ''
  return ''
})

async function copy(text: string) {
  await copyText(text)
  message.success(t('inbounds.copied'))
}

const tagOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(form.tag.trim()))
const touched = ref(false)

const listenOptions = computed(() => [
  { label: t('inbounds.listenAll'), value: '::' },
  { label: '0.0.0.0', value: '0.0.0.0' },
  { label: t('inbounds.listenLocal'), value: '127.0.0.1' },
])
const alpnOptions = ['h3', 'h2', 'http/1.1'].map((v) => ({ label: v, value: v }))
const flowOptions = computed(() => [
  { label: t('inbounds.flowVision'), value: 'xtls-rprx-vision' },
  { label: t('inbounds.flowNone'), value: '' },
])

async function submit() {
  touched.value = true
  if (isNew.value && !tagOk.value) return
  const body = JSON.parse(JSON.stringify(form)) as InboundForm
  body.tag = body.tag.trim()
  body.tls.key = keyInput.value.trim() || (keyStored.value ? MASK : '')
  body.tls.private_key = privInput.value.trim() || (privStored.value ? MASK : '')
  if (body.tls.mode === 'none') body.flow = '' // Vision needs TLS
  const payload = {
    form: body,
    assign_users: assignUsers.value,
    export: { ...exp, name: exp.name?.trim() || undefined, host: exp.host?.trim() || undefined, port: exp.port || undefined },
  }
  busy.value = true
  try {
    const res = isNew.value
      ? await post<{ result: ApplyResult }>('/inbounds', payload)
      : await put<{ result: ApplyResult }>(`/inbounds/${encodeURIComponent(props.item!.form.tag)}`, payload)
    feedback.success(res.result)
    emit('update:show', false)
    emit('saved')
  } catch (e) {
    if (isNetworkError(e)) emit('update:show', false)
    feedback.failure(e, () => emit('saved'))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NDrawer :show="show" :width="isMobile ? '100%' : 560" placement="right" :mask-closable="!busy" @update:show="emit('update:show', $event)">
    <NDrawerContent :title="isNew ? t('inbounds.add') : t('inbounds.edit', { tag: item?.form.tag })" closable>
      <NAlert v-if="item && item.extra.length" type="info" :show-icon="false" class="gap">
        {{ t('inbounds.extraKept', { keys: item.extra.join(', ') }) }}
      </NAlert>
      <NForm label-placement="top" :show-feedback="false" class="form" @submit.prevent="submit">
        <h4>{{ t('inbounds.basic') }}</h4>
        <NFormItem v-if="isNew" :label="t('inbounds.type')">
          <NRadioGroup :value="form.type" @update:value="switchType">
            <NRadioButton value="vless">VLESS</NRadioButton>
            <NRadioButton value="hysteria2">Hysteria2</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('inbounds.tag')">
          <div class="field">
            <NInput
              v-model:value="form.tag"
              :disabled="!isNew"
              :status="touched && isNew && !tagOk ? 'error' : undefined"
              :placeholder="form.type === 'vless' ? 'vless-in' : 'hy2-in'"
              maxlength="64"
            />
            <NText depth="3" class="hint">{{ touched && isNew && !tagOk ? t('inbounds.tagRule') : t('inbounds.tagHint') }}</NText>
          </div>
        </NFormItem>
        <div class="row">
          <NFormItem :label="t('inbounds.listenAddr')" class="grow">
            <NSelect v-model:value="form.listen" :options="listenOptions" filterable tag />
          </NFormItem>
          <NFormItem :label="t('inbounds.port')" class="port">
            <NInputNumber v-model:value="form.listen_port" :min="1" :max="65535" :precision="0" :show-button="false" />
          </NFormItem>
        </div>
        <NText depth="3" class="hint below">
          {{ form.type === 'hysteria2' ? t('inbounds.portHintHy2') : t('inbounds.portHintVless') }}
        </NText>

        <NDivider />
        <h4>{{ form.tls.mode === 'reality' ? t('inbounds.realityTitle') : t('inbounds.tlsTitle') }}</h4>
        <NFormItem v-if="form.type === 'vless'" :label="t('inbounds.mode')">
          <NRadioGroup v-model:value="form.tls.mode">
            <NRadioButton value="reality">{{ t('inbounds.secReality') }}</NRadioButton>
            <NRadioButton value="tls">{{ t('inbounds.secTls') }}</NRadioButton>
            <NRadioButton value="none">{{ t('inbounds.secNone') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>

        <template v-if="form.tls.mode !== 'none'">
          <NFormItem :label="t('inbounds.serverName')">
            <div class="field">
              <NInput v-model:value="form.tls.server_name" placeholder="www.example.com" />
              <NText depth="3" class="hint">
                {{ form.tls.mode === 'reality' ? t('inbounds.serverNameHintReality') : t('inbounds.serverNameHintTls') }}
              </NText>
            </div>
          </NFormItem>
        </template>

        <template v-if="form.tls.mode === 'reality'">
          <NFormItem :label="t('inbounds.handshake')">
            <div class="field">
              <NInputGroup>
                <NInput v-model:value="form.tls.handshake_server" placeholder="www.cloudflare.com" />
                <NInputNumber
                  v-model:value="form.tls.handshake_port"
                  :min="1"
                  :max="65535"
                  :show-button="false"
                  style="width: 100px"
                />
              </NInputGroup>
              <NText depth="3" class="hint">{{ t('inbounds.handshakeHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="t('inbounds.privateKey')">
            <div class="field">
              <NInputGroup>
                <NInput
                  v-model:value="privInput"
                  type="password"
                  show-password-on="click"
                  :placeholder="privStored ? t('inbounds.keyKept') : ''"
                  class="mono-input"
                />
                <NButton :loading="generating === 'reality_keypair'" @click="genKeyPair">{{ t('inbounds.genKeyPair') }}</NButton>
              </NInputGroup>
              <NText v-if="!isNew" depth="3" class="hint">{{ t('inbounds.keyPairHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="t('inbounds.publicKey')">
            <NInputGroup v-if="publicKey">
              <NInput :value="publicKey" readonly class="mono-input" />
              <NButton @click="copy(publicKey)">{{ t('common.copy') }}</NButton>
            </NInputGroup>
            <NText v-else depth="3">{{ t('inbounds.publicKeyLater') }}</NText>
          </NFormItem>
          <NFormItem :label="t('inbounds.shortIds')">
            <div class="field">
              <NSpace :size="8" align="center">
                <NDynamicTags v-model:value="form.tls.short_ids as string[]" />
                <NButton size="small" :loading="generating === 'short_id'" @click="genShortId">{{ t('inbounds.genShortId') }}</NButton>
              </NSpace>
              <NText depth="3" class="hint">{{ t('inbounds.shortIdsHint') }}</NText>
            </div>
          </NFormItem>
        </template>

        <template v-if="form.tls.mode === 'tls'">
          <NFormItem :label="t('inbounds.alpn')">
            <div class="field">
              <NSelect v-model:value="form.tls.alpn" :options="alpnOptions" multiple filterable tag :placeholder="t('inbounds.alpnHint')" />
            </div>
          </NFormItem>
          <NFormItem :label="t('inbounds.certSource')">
            <NSpace align="center" :size="12">
              <NRadioGroup v-model:value="form.tls.cert_source">
                <NRadioButton value="inline">{{ t('inbounds.certInline') }}</NRadioButton>
                <NRadioButton value="file">{{ t('inbounds.certFile') }}</NRadioButton>
              </NRadioGroup>
              <NButton v-if="form.tls.cert_source === 'inline'" :loading="generating === 'self_signed'" @click="genSelfSigned">
                {{ t('inbounds.genSelfSigned') }}
              </NButton>
            </NSpace>
          </NFormItem>
          <template v-if="form.tls.cert_source === 'file'">
            <NFormItem :label="t('inbounds.certPath')">
              <NInput v-model:value="form.tls.certificate_path" placeholder="/etc/sing-box/cert/cert.pem" class="mono-input" />
            </NFormItem>
            <NFormItem :label="t('inbounds.keyPath')">
              <div class="field">
                <NInput v-model:value="form.tls.key_path" placeholder="/etc/sing-box/cert/private.key" class="mono-input" />
                <NText depth="3" class="hint">{{ t('inbounds.pathHint') }}</NText>
              </div>
            </NFormItem>
          </template>
          <template v-else>
            <NFormItem :label="t('inbounds.certificate')">
              <NInput
                v-model:value="form.tls.certificate"
                type="textarea"
                :autosize="{ minRows: 3, maxRows: 6 }"
                placeholder="-----BEGIN CERTIFICATE-----"
                class="mono-input"
              />
            </NFormItem>
            <NFormItem :label="t('inbounds.key')">
              <NInput
                v-model:value="keyInput"
                type="textarea"
                :autosize="{ minRows: 2, maxRows: 5 }"
                :placeholder="keyStored ? t('inbounds.keyKept') : '-----BEGIN PRIVATE KEY-----'"
                class="mono-input"
              />
            </NFormItem>
          </template>
        </template>

        <template v-if="form.type === 'hysteria2'">
          <NDivider />
          <h4>{{ t('inbounds.hy2Title') }}</h4>
          <NFormItem :label="t('inbounds.obfs')">
            <div class="field">
              <NInputGroup>
                <NInput v-model:value="form.obfs_password" :placeholder="t('inbounds.secNone')" class="mono-input" />
                <NButton :loading="generating === 'password'" @click="genObfs">{{ t('inbounds.genPassword') }}</NButton>
              </NInputGroup>
              <NText depth="3" class="hint">{{ t('inbounds.obfsHint') }}</NText>
            </div>
          </NFormItem>
          <div class="row">
            <NFormItem :label="t('inbounds.up')" class="grow">
              <NInputNumber v-model:value="form.up_mbps" :min="0" :precision="0" />
            </NFormItem>
            <NFormItem :label="t('inbounds.down')" class="grow">
              <NInputNumber v-model:value="form.down_mbps" :min="0" :precision="0" />
            </NFormItem>
          </div>
          <NText depth="3" class="hint below">{{ t('inbounds.bandwidthHint') }}</NText>
          <NFormItem :label="t('inbounds.masquerade')">
            <div class="field">
              <NInput v-model:value="form.masquerade" placeholder="https://www.bing.com" />
              <NText depth="3" class="hint">{{ t('inbounds.masqueradeHint') }}</NText>
            </div>
          </NFormItem>
        </template>

        <template v-if="form.type === 'vless' && form.tls.mode !== 'none'">
          <NDivider />
          <h4>{{ t('inbounds.vlessTitle') }}</h4>
          <NFormItem :label="t('inbounds.flow')">
            <div class="field">
              <NSelect v-model:value="form.flow" :options="flowOptions" />
              <NText v-if="!isNew" depth="3" class="hint">{{ t('inbounds.flowHint') }}</NText>
            </div>
          </NFormItem>
        </template>

        <NDivider />
        <h4>{{ t('inbounds.exportTitle') }}</h4>
        <NFormItem :label="t('inbounds.exportName')">
          <NInput v-model:value="exp.name" :placeholder="form.tag || t('inbounds.tag')" clearable />
        </NFormItem>
        <div class="row">
          <NFormItem :label="t('inbounds.exportHost')" class="grow">
            <NInput v-model:value="exp.host" :placeholder="publicHost || '—'" clearable />
          </NFormItem>
          <NFormItem :label="t('inbounds.exportPort')" class="port">
            <NInputNumber
              v-model:value="exp.port"
              :min="1"
              :max="65535"
              :precision="0"
              :show-button="false"
              :placeholder="String(form.listen_port || '')"
              clearable
            />
          </NFormItem>
        </div>
        <NText depth="3" class="hint below">{{ t('inbounds.exportPortHint') }}</NText>
        <NFormItem v-if="form.tls.mode === 'tls'" :show-label="false">
          <NSpace align="center" :size="8">
            <NSwitch v-model:value="exp.insecure" size="small" />
            <span>{{ t('inbounds.insecure') }}</span>
            <NText depth="3" class="hint inline">{{ t('inbounds.insecureHint') }}</NText>
          </NSpace>
        </NFormItem>

        <NFormItem :show-label="false">
          <NSpace align="center" :size="8">
            <NSwitch v-model:value="exp.hidden" size="small" />
            <span>{{ t('inbounds.notExported') }}</span>
            <NText depth="3" class="hint inline">{{ t('inbounds.hiddenHint') }}</NText>
          </NSpace>
        </NFormItem>

        <template v-if="isNew && userCount > 0">
          <NDivider />
          <NCheckbox v-model:checked="assignUsers">{{ t('inbounds.assignUsers', { n: userCount }) }}</NCheckbox>
          <NText depth="3" class="hint">{{ t('inbounds.assignHint') }}</NText>
        </template>
      </NForm>

      <template #footer>
        <NSpace justify="end">
          <NButton :disabled="busy" @click="emit('update:show', false)">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="busy" @click="submit">
            {{ isNew ? t('inbounds.create') : t('inbounds.saveApply') }}
          </NButton>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>

<style scoped>
.gap {
  margin-bottom: 12px;
}
.form h4 {
  margin: 0 0 10px;
  font-size: 14px;
  font-weight: 600;
}
.form :deep(.n-form-item) {
  margin-bottom: 14px;
}
.field {
  width: 100%;
}
.hint {
  display: block;
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}
.hint.below {
  margin: -8px 0 14px;
}
.hint.inline {
  display: inline;
  margin: 0;
}
.row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.grow {
  flex: 1 1 200px;
}
.port {
  flex: 0 1 130px;
}
.mono-input :deep(input),
.mono-input :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
:deep(.n-divider) {
  margin: 6px 0 16px;
}
</style>
