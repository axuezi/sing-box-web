<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NSelect, NSpace, NSwitch, NText } from 'naive-ui'
import { post, put, type ApplyResult, type DnsServerForm, type DnsServerItem, type DnsServerType } from '../../api'
import { isNetworkError, useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'

const props = defineProps<{ show: boolean; item: DnsServerItem | null; outbounds: string[]; servers: string[] }>()
const emit = defineEmits<{ 'update:show': [boolean]; saved: [] }>()
const { t } = useI18n()
const feedback = useApplyFeedback()
const isMobile = useIsMobile()

const TYPES: DnsServerType[] = ['https', 'tls', 'quic', 'h3', 'udp', 'tcp', 'local']
const DEFAULT_PORT: Record<string, number> = { udp: 53, tcp: 53, tls: 853, quic: 853, https: 443, h3: 443 }

function blank(type: DnsServerType): DnsServerForm {
  // server_port null = the type's default; the input then shows it as a placeholder.
  return { tag: '', type, server: '', server_port: null as unknown as number, path: '', server_name: '', insecure: false, detour: '', resolver: '' }
}

const form = reactive<DnsServerForm>(blank('https'))
const busy = ref(false)
const touched = ref(false)
const isNew = computed(() => !props.item)

function reset() {
  Object.assign(form, props.item ? JSON.parse(JSON.stringify(props.item.form)) : blank('https'))
  // 0 means "the type's default port": show the placeholder, not a 0.
  if (!form.server_port) form.server_port = null as unknown as number
  touched.value = false
}
watch(
  () => props.show,
  (open) => open && reset(),
)

function switchType(type: DnsServerType) {
  const keep = { tag: form.tag, server: form.server, detour: form.detour, resolver: form.resolver }
  Object.assign(form, blank(type), type === 'local' ? { tag: form.tag } : keep)
}

const remote = computed(() => form.type !== 'local')
const hasTls = computed(() => ['tls', 'https', 'quic', 'h3'].includes(form.type))
const hasPath = computed(() => form.type === 'https' || form.type === 'h3')
const isHostName = computed(() => /[a-z]/i.test(form.server) && !form.server.includes(':'))

const typeOptions = computed(() => TYPES.map((v) => ({ label: t(`dns.types.${v}`), value: v })))
const detourOptions = computed(() => [{ label: t('outbounds.detourNone'), value: '' }, ...props.outbounds.map((x) => ({ label: x, value: x }))])
const resolverOptions = computed(() => [
  { label: t('dns.resolverNone'), value: '' },
  ...props.servers.filter((x) => x !== form.tag).map((x) => ({ label: x, value: x })),
])

const tagOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(form.tag.trim()))
const serverOk = computed(() => !remote.value || /^[A-Za-z0-9:.[\]-]+$/.test(form.server.trim()))
const pathOk = computed(() => !hasPath.value || !form.path.trim() || form.path.trim().startsWith('/'))

async function submit() {
  touched.value = true
  if ((isNew.value && !tagOk.value) || !serverOk.value || !pathOk.value) return
  const body = JSON.parse(JSON.stringify(form)) as DnsServerForm
  body.tag = body.tag.trim()
  body.server = body.server.trim().replace(/^\[(.*)\]$/, '$1')
  body.server_port ||= 0
  busy.value = true
  try {
    const res = isNew.value
      ? await post<{ result: ApplyResult }>('/dns/servers', { form: body })
      : await put<{ result: ApplyResult }>(`/dns/servers/${encodeURIComponent(props.item!.form.tag)}`, { form: body })
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
  <NDrawer :show="show" :width="isMobile ? '100%' : 520" placement="right" :mask-closable="!busy" @update:show="emit('update:show', $event)">
    <NDrawerContent :title="isNew ? t('dns.add') : t('dns.edit', { tag: item?.form.tag })" closable>
      <NAlert v-if="item && item.extra.length" type="info" :show-icon="false" class="gap">
        {{ t('inbounds.extraKept', { keys: item.extra.join(', ') }) }}
      </NAlert>
      <NForm label-placement="top" :show-feedback="false" class="form" @submit.prevent="submit">
        <NFormItem v-if="isNew" :label="t('outbounds.type')">
          <div class="field">
            <NSelect :value="form.type" :options="typeOptions" @update:value="switchType" />
            <NText depth="3" class="hint">{{ t(`dns.typeHints.${form.type}`) }}</NText>
          </div>
        </NFormItem>
        <NFormItem :label="t('outbounds.tag')">
          <div class="field">
            <NInput
              v-model:value="form.tag"
              :disabled="!isNew"
              :status="touched && isNew && !tagOk ? 'error' : undefined"
              placeholder="cloudflare-doh"
              maxlength="64"
            />
            <NText depth="3" class="hint">{{ touched && isNew && !tagOk ? t('inbounds.tagRule') : t('dns.tagHint') }}</NText>
          </div>
        </NFormItem>

        <template v-if="remote">
          <div class="row">
            <NFormItem :label="t('outbounds.server')" class="grow">
              <NInput v-model:value="form.server" :status="touched && !serverOk ? 'error' : undefined" placeholder="1.1.1.1 / dns.google" />
            </NFormItem>
            <NFormItem :label="t('inbounds.port')" class="port">
              <NInputNumber
                v-model:value="form.server_port"
                :min="0"
                :max="65535"
                :precision="0"
                :show-button="false"
                :placeholder="String(DEFAULT_PORT[form.type] ?? '')"
                clearable
              />
            </NFormItem>
          </div>
          <NFormItem v-if="hasPath" :label="t('dns.path')">
            <NInput v-model:value="form.path" :status="touched && !pathOk ? 'error' : undefined" placeholder="/dns-query" class="mono-input" />
          </NFormItem>
          <NFormItem v-if="hasTls" :label="t('inbounds.serverName')">
            <div class="field">
              <NInput v-model:value="form.server_name" :placeholder="isHostName ? form.server : 'cloudflare-dns.com'" />
              <NText depth="3" class="hint">{{ t('dns.sniHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem v-if="hasTls" :show-label="false">
            <NSpace align="center" :size="8">
              <NSwitch v-model:value="form.insecure" size="small" />
              <span>{{ t('outbounds.insecure') }}</span>
            </NSpace>
          </NFormItem>
          <NFormItem :label="t('dns.resolver')">
            <div class="field">
              <NSelect v-model:value="form.resolver" :options="resolverOptions" />
              <NText depth="3" class="hint">{{ isHostName && !form.resolver ? t('dns.resolverNeeded') : t('dns.resolverHint') }}</NText>
            </div>
          </NFormItem>
          <NFormItem :label="t('outbounds.detour')">
            <div class="field">
              <NSelect v-model:value="form.detour" :options="detourOptions" />
              <NText depth="3" class="hint">{{ t('dns.detourHint') }}</NText>
            </div>
          </NFormItem>
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
.mono-input :deep(input) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
