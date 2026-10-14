<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDivider, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputGroup, NInputNumber, NRadioButton,
  NRadioGroup, NSelect, NSpace, NSwitch, NText,
} from 'naive-ui'
import { post, put, type ApplyResult, type OutboundForm, type OutboundItem, type OutboundType } from '../../api'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'

const props = defineProps<{ show: boolean; item: OutboundItem | null; tags: string[]; dnsServers: string[] }>()
const emit = defineEmits<{ 'update:show': [boolean]; saved: [] }>()
const { t } = useI18n()
const feedback = useApplyFeedback()
const isMobile = useIsMobile()

/** Secrets come back masked; sending the mask keeps the stored value. */
const MASK = '••••••••'

const TYPES: OutboundType[] = ['direct', 'socks', 'http', 'shadowsocks', 'vless', 'trojan', 'hysteria2']
const METHODS = [
  '2022-blake3-aes-128-gcm', '2022-blake3-aes-256-gcm', '2022-blake3-chacha20-poly1305',
  'aes-128-gcm', 'aes-256-gcm', 'chacha20-ietf-poly1305', 'xchacha20-ietf-poly1305', 'none',
]
const STRATEGIES = ['prefer_ipv4', 'prefer_ipv6', 'ipv4_only', 'ipv6_only']
const FINGERPRINTS = ['chrome', 'firefox', 'edge', 'safari', 'ios', 'android', 'random', 'randomized']

function blank(type: OutboundType): OutboundForm {
  return {
    tag: '',
    type,
    server: '',
    server_port: type === 'socks' ? 1080 : type === 'http' ? 8080 : type === 'shadowsocks' ? 8388 : 443,
    username: '',
    password: '',
    method: '2022-blake3-aes-128-gcm',
    uuid: '',
    flow: '',
    tls: {
      mode: type === 'vless' ? 'reality' : type === 'trojan' || type === 'hysteria2' ? 'tls' : 'none',
      server_name: '',
      insecure: false,
      alpn: [],
      utls: type === 'vless' ? 'chrome' : '',
      public_key: '',
      short_id: '',
    },
    up_mbps: 0,
    down_mbps: 0,
    obfs_password: '',
    detour: '',
    bind_interface: '',
    inet4_bind_address: '',
    inet6_bind_address: '',
    resolver: '',
    resolver_strategy: '',
  }
}

const form = reactive<OutboundForm>(blank('direct'))
// Credential inputs start empty; empty + a stored secret = keep it.
const pwInput = ref('')
const uuidInput = ref('')
const obfsInput = ref('')
const busy = ref(false)
const touched = ref(false)

const isNew = computed(() => !props.item)
const stored = (v?: string) => v === MASK
const pwStored = computed(() => stored(props.item?.form.password))
const uuidStored = computed(() => stored(props.item?.form.uuid))
const obfsStored = computed(() => stored(props.item?.form.obfs_password))

function reset() {
  const src = props.item ? (JSON.parse(JSON.stringify(props.item.form)) as OutboundForm) : blank('direct')
  src.tls.alpn ??= []
  Object.assign(form, src)
  pwInput.value = uuidInput.value = obfsInput.value = ''
  touched.value = false
}
watch(
  () => props.show,
  (open) => open && reset(),
)

function switchType(type: OutboundType) {
  const keep = { tag: form.tag, detour: form.detour, resolver: form.resolver, resolver_strategy: form.resolver_strategy }
  Object.assign(form, blank(type), keep)
  pwInput.value = uuidInput.value = obfsInput.value = ''
}

const remote = computed(() => form.type !== 'direct')
const hasAuth = computed(() => form.type === 'socks' || form.type === 'http')
const hasPassword = computed(() => ['socks', 'http', 'shadowsocks', 'trojan', 'hysteria2'].includes(form.type))
const tlsChoice = computed(() => form.type === 'http' || form.type === 'vless')
const hasTls = computed(() => ['http', 'vless', 'trojan', 'hysteria2'].includes(form.type))

const typeOptions = computed(() => TYPES.map((v) => ({ label: t(`outbounds.types.${v}`), value: v })))
const detourOptions = computed(() => [
  { label: t('outbounds.detourNone'), value: '' },
  ...props.tags.filter((x) => x !== form.tag).map((x) => ({ label: x, value: x })),
])
const resolverOptions = computed(() => [
  { label: t('outbounds.resolverDefault'), value: '' },
  ...props.dnsServers.map((x) => ({ label: x, value: x })),
])
const strategyOptions = computed(() => [
  { label: t('dns.strategies.default'), value: '' },
  ...STRATEGIES.map((v) => ({ label: t(`dns.strategies.${v}`), value: v })),
])
const methodOptions = METHODS.map((v) => ({ label: v, value: v }))
const fpOptions = computed(() => [{ label: t('outbounds.utlsOff'), value: '' }, ...FINGERPRINTS.map((v) => ({ label: v, value: v }))])
const alpnOptions = ['h3', 'h2', 'http/1.1'].map((v) => ({ label: v, value: v }))
const flowOptions = computed(() => [
  { label: t('inbounds.flowNone'), value: '' },
  { label: t('inbounds.flowVision'), value: 'xtls-rprx-vision' },
])

// Client-side checks for the common slips; the server checks everything.
const tagOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(form.tag.trim()))
const serverOk = computed(() => !remote.value || /^[A-Za-z0-9:.[\]-]+$/.test(form.server.trim()))
const pwMissing = computed(
  () =>
    (['trojan', 'hysteria2'].includes(form.type) || (form.type === 'shadowsocks' && form.method !== 'none')) &&
    !pwInput.value && !pwStored.value,
)
const uuidOk = computed(
  () => form.type !== 'vless' || (!uuidInput.value.trim() && uuidStored.value) ||
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(uuidInput.value.trim()),
)

function newUUID() {
  uuidInput.value = crypto.randomUUID()
}

async function submit() {
  touched.value = true
  if ((isNew.value && !tagOk.value) || !serverOk.value || pwMissing.value || !uuidOk.value) return
  const body = JSON.parse(JSON.stringify(form)) as OutboundForm
  body.tag = body.tag.trim()
  body.server = body.server.trim().replace(/^\[(.*)\]$/, '$1')
  body.password = pwInput.value || (pwStored.value ? MASK : '')
  body.uuid = uuidInput.value.trim() || (uuidStored.value ? MASK : '')
  body.obfs_password = obfsInput.value || (obfsStored.value && form.obfs_password ? MASK : '')
  if (body.tls.mode === 'none') body.flow = ''
  busy.value = true
  try {
    const res = isNew.value
      ? await post<{ result: ApplyResult }>('/outbounds', { form: body })
      : await put<{ result: ApplyResult }>(`/outbounds/${encodeURIComponent(props.item!.form.tag)}`, { form: body })
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
    <NDrawerContent :title="isNew ? t('outbounds.add') : t('outbounds.edit', { tag: item?.form.tag })" closable>
      <NAlert v-if="item && item.extra.length" type="info" :show-icon="false" class="gap">
        {{ t('inbounds.extraKept', { keys: item.extra.join(', ') }) }}
      </NAlert>
      <NForm label-placement="top" :show-feedback="false" class="form" @submit.prevent="submit">
        <h4>{{ t('inbounds.basic') }}</h4>
        <NFormItem v-if="isNew" :label="t('outbounds.type')">
          <div class="field">
            <NSelect :value="form.type" :options="typeOptions" @update:value="switchType" />
            <NText depth="3" class="hint">{{ t(`outbounds.typeHints.${form.type}`) }}</NText>
          </div>
        </NFormItem>
        <NFormItem :label="t('outbounds.tag')">
          <div class="field">
            <NInput
              v-model:value="form.tag"
              :disabled="!isNew"
              :status="touched && isNew && !tagOk ? 'error' : undefined"
              :placeholder="form.type === 'direct' ? 'direct-v4' : 'proxy-out'"
              maxlength="64"
            />
            <NText depth="3" class="hint">{{ touched && isNew && !tagOk ? t('inbounds.tagRule') : t('outbounds.tagHint') }}</NText>
          </div>
        </NFormItem>

        <template v-if="remote">
          <div class="row">
            <NFormItem :label="t('outbounds.server')" class="grow">
              <NInput
                v-model:value="form.server"
                :status="touched && !serverOk ? 'error' : undefined"
                placeholder="proxy.example.com / 203.0.113.5"
              />
            </NFormItem>
            <NFormItem :label="t('inbounds.port')" class="port">
              <NInputNumber v-model:value="form.server_port" :min="1" :max="65535" :precision="0" :show-button="false" />
            </NFormItem>
          </div>

          <NFormItem v-if="hasAuth" :label="t('outbounds.username')">
            <NInput v-model:value="form.username" :placeholder="t('outbounds.optional')" />
          </NFormItem>
          <NFormItem v-if="form.type === 'shadowsocks'" :label="t('outbounds.method')">
            <NSelect v-model:value="form.method" :options="methodOptions" />
          </NFormItem>
          <NFormItem v-if="hasPassword" :label="t('outbounds.password')">
            <div class="field">
              <NInput
                v-model:value="pwInput"
                type="password"
                show-password-on="click"
                :status="touched && pwMissing ? 'error' : undefined"
                :placeholder="pwStored ? t('outbounds.kept') : hasAuth ? t('outbounds.optional') : ''"
                class="mono-input"
              />
            </div>
          </NFormItem>
          <NFormItem v-if="form.type === 'vless'" label="UUID">
            <div class="field">
              <NInputGroup>
                <NInput
                  v-model:value="uuidInput"
                  :status="touched && !uuidOk ? 'error' : undefined"
                  :placeholder="uuidStored ? t('outbounds.kept') : 'xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx'"
                  class="mono-input"
                />
                <NButton @click="newUUID">{{ t('outbounds.generate') }}</NButton>
              </NInputGroup>
              <NText depth="3" class="hint">{{ t('outbounds.uuidHint') }}</NText>
            </div>
          </NFormItem>
        </template>

        <template v-if="hasTls">
          <NDivider />
          <h4>{{ form.tls.mode === 'reality' ? t('inbounds.realityTitle') : t('inbounds.tlsTitle') }}</h4>
          <NFormItem v-if="tlsChoice" :label="t('inbounds.mode')">
            <NRadioGroup v-model:value="form.tls.mode">
              <NRadioButton v-if="form.type === 'vless'" value="reality">{{ t('inbounds.secReality') }}</NRadioButton>
              <NRadioButton value="tls">{{ t('inbounds.secTls') }}</NRadioButton>
              <NRadioButton value="none">{{ t('inbounds.secNone') }}</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <template v-if="form.tls.mode !== 'none'">
            <NFormItem :label="t('inbounds.serverName')">
              <div class="field">
                <NInput v-model:value="form.tls.server_name" :placeholder="form.server || 'www.example.com'" />
                <NText depth="3" class="hint">
                  {{ form.tls.mode === 'reality' ? t('outbounds.sniHintReality') : t('outbounds.sniHint') }}
                </NText>
              </div>
            </NFormItem>
            <template v-if="form.tls.mode === 'reality'">
              <NFormItem :label="t('inbounds.publicKey')">
                <NInput v-model:value="form.tls.public_key" class="mono-input" :placeholder="t('outbounds.publicKeyHint')" />
              </NFormItem>
              <NFormItem label="Short ID">
                <NInput v-model:value="form.tls.short_id" class="mono-input" placeholder="0123abcd" />
              </NFormItem>
            </template>
            <NFormItem :label="t('outbounds.utls')">
              <div class="field">
                <NSelect v-model:value="form.tls.utls" :options="fpOptions" />
                <NText depth="3" class="hint">
                  {{ form.tls.mode === 'reality' ? t('outbounds.utlsRealityHint') : t('outbounds.utlsHint') }}
                </NText>
              </div>
            </NFormItem>
            <NFormItem v-if="form.tls.mode === 'tls'" :label="t('inbounds.alpn')">
              <NSelect v-model:value="form.tls.alpn" :options="alpnOptions" multiple filterable tag :placeholder="t('outbounds.alpnHint')" />
            </NFormItem>
            <NFormItem v-if="form.tls.mode === 'tls'" :show-label="false">
              <NSpace align="center" :size="8">
                <NSwitch v-model:value="form.tls.insecure" size="small" />
                <span>{{ t('outbounds.insecure') }}</span>
                <NText depth="3" class="hint inline">{{ t('outbounds.insecureHint') }}</NText>
              </NSpace>
            </NFormItem>
          </template>
        </template>

        <template v-if="form.type === 'vless' && form.tls.mode !== 'none'">
          <NFormItem :label="t('inbounds.flow')">
            <div class="field">
              <NSelect v-model:value="form.flow" :options="flowOptions" />
              <NText depth="3" class="hint">{{ t('outbounds.flowHint') }}</NText>
            </div>
          </NFormItem>
        </template>

        <template v-if="form.type === 'hysteria2'">
          <NDivider />
          <h4>{{ t('inbounds.hy2Title') }}</h4>
          <NFormItem :label="t('inbounds.obfs')">
            <div class="field">
              <NInput
                v-model:value="obfsInput"
                type="password"
                show-password-on="click"
                :placeholder="obfsStored && form.obfs_password ? t('outbounds.kept') : t('inbounds.secNone')"
                class="mono-input"
              />
              <NSpace v-if="obfsStored && form.obfs_password" align="center" :size="8" class="hint">
                <NButton size="tiny" @click="(form.obfs_password = ''), (obfsInput = '')">{{ t('outbounds.obfsOff') }}</NButton>
              </NSpace>
              <NText depth="3" class="hint">{{ t('outbounds.obfsHint') }}</NText>
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
          <NText depth="3" class="hint below">{{ t('outbounds.bandwidthHint') }}</NText>
        </template>

        <NDivider />
        <h4>{{ t('outbounds.dialTitle') }}</h4>
        <NFormItem :label="t('outbounds.detour')">
          <div class="field">
            <NSelect v-model:value="form.detour" :options="detourOptions" />
            <NText depth="3" class="hint">{{ t('outbounds.detourHint') }}</NText>
          </div>
        </NFormItem>
        <div class="row">
          <NFormItem :label="t('outbounds.inet4')" class="grow">
            <NInput v-model:value="form.inet4_bind_address" :placeholder="t('outbounds.auto')" class="mono-input" />
          </NFormItem>
          <NFormItem :label="t('outbounds.inet6')" class="grow">
            <NInput v-model:value="form.inet6_bind_address" :placeholder="t('outbounds.auto')" class="mono-input" />
          </NFormItem>
        </div>
        <NText depth="3" class="hint below">{{ t('outbounds.bindHint') }}</NText>
        <NFormItem :label="t('outbounds.iface')">
          <NInput v-model:value="form.bind_interface" :placeholder="t('outbounds.auto')" class="mono-input" />
        </NFormItem>
        <div class="row">
          <NFormItem :label="t('outbounds.resolver')" class="grow">
            <NSelect v-model:value="form.resolver" :options="resolverOptions" />
          </NFormItem>
          <NFormItem :label="t('outbounds.strategy')" class="grow">
            <NSelect v-model:value="form.resolver_strategy" :options="strategyOptions" :disabled="!form.resolver" />
          </NFormItem>
        </div>
        <NText depth="3" class="hint below">{{ t('outbounds.resolverHint') }}</NText>
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
