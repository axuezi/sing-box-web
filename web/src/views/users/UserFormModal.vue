<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCheckbox, NCheckboxGroup, NDatePicker, NForm, NFormItem, NInput, NInputNumber, NModal, NSpace, NSwitch,
  NTag, NText,
} from 'naive-ui'
import type { User, UserInput } from '../../api'

const props = defineProps<{ show: boolean; user: User | null; inbounds: { tag: string; type: string }[]; busy: boolean }>()
const emit = defineEmits<{
  'update:show': [boolean]
  submit: [UserInput]
}>()
const { t } = useI18n()

const GB = 1024 ** 3

const form = reactive({
  name: '',
  note: '',
  enabled: true,
  inbounds: [] as string[],
  expiresDay: null as number | null, // start of the chosen day, local time (ms)
  quotaGb: 0 as number | null,
  resetDay: 1 as number | null,
})
const touched = ref(false)
let initialDay: number | null = null

function startOfDay(ms: number): number {
  const d = new Date(ms)
  d.setHours(0, 0, 0, 0)
  return d.getTime()
}

function monthsFromNow(n: number): number {
  const d = new Date()
  d.setMonth(d.getMonth() + n)
  return startOfDay(d.getTime())
}

const shortcuts = computed(() => ({
  [t('users.in1m')]: () => monthsFromNow(1),
  [t('users.in3m')]: () => monthsFromNow(3),
  [t('users.in1y')]: () => monthsFromNow(12),
}))

/** The expiry to send: unchanged dates keep their exact time (a renewal may
 * have set one); a newly picked date expires at the end of that day. */
function expiresAt(): number {
  if (form.expiresDay === null) return 0
  if (props.user?.expires_at && form.expiresDay === initialDay) {
    return Math.floor(new Date(props.user.expires_at).getTime() / 1000)
  }
  const end = new Date(form.expiresDay)
  end.setHours(23, 59, 59, 0)
  return Math.floor(end.getTime() / 1000)
}

watch(
  () => props.show,
  (open) => {
    if (!open) return
    touched.value = false
    form.name = props.user?.name ?? ''
    form.note = props.user?.note ?? ''
    form.enabled = props.user?.enabled ?? true
    form.inbounds = props.user ? [...props.user.inbounds] : props.inbounds.map((i) => i.tag)
    initialDay = props.user?.expires_at ? startOfDay(new Date(props.user.expires_at).getTime()) : null
    form.expiresDay = initialDay
    form.quotaGb = props.user ? Math.round((props.user.quota_bytes / GB) * 100) / 100 : 0
    form.resetDay = props.user?.reset_day ?? 1
  },
)

const nameOk = computed(() => /^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$/.test(form.name.trim()))

function submit() {
  touched.value = true
  if (!nameOk.value) return
  emit('submit', {
    name: form.name.trim(),
    note: form.note,
    enabled: form.enabled,
    inbounds: form.inbounds,
    expires_at: expiresAt(),
    quota_bytes: Math.round((form.quotaGb ?? 0) * GB),
    reset_day: form.resetDay ?? 1,
  })
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    :title="user ? t('users.edit') : t('users.add')"
    :style="{ width: '440px', maxWidth: 'calc(100vw - 32px)' }"
    :mask-closable="!busy"
    @update:show="emit('update:show', $event)"
  >
    <NForm label-placement="top" @submit.prevent="submit">
      <NFormItem
        :label="t('users.name')"
        :validation-status="touched && !nameOk ? 'error' : undefined"
        :feedback="touched && !nameOk ? t('users.nameRule') : undefined"
      >
        <NInput v-model:value="form.name" :placeholder="t('users.namePlaceholder')" maxlength="64" @keyup.enter="submit" />
      </NFormItem>
      <NFormItem :label="t('users.note')">
        <NInput v-model:value="form.note" type="textarea" :autosize="{ minRows: 1, maxRows: 4 }" maxlength="500" placeholder="" />
      </NFormItem>
      <NFormItem :label="t('users.inbounds')">
        <NCheckboxGroup v-model:value="form.inbounds">
          <NSpace vertical :size="6">
            <NCheckbox v-for="ib in inbounds" :key="ib.tag" :value="ib.tag">
              {{ ib.tag }} <NTag size="tiny" :bordered="false" class="type">{{ ib.type }}</NTag>
            </NCheckbox>
          </NSpace>
        </NCheckboxGroup>
      </NFormItem>
      <NText v-if="form.inbounds.length === 0" type="warning" class="warn">{{ t('users.noInbounds') }}</NText>
      <NFormItem :label="t('users.expiresAt')">
        <div class="field">
          <NDatePicker
            v-model:value="form.expiresDay"
            type="date"
            clearable
            :shortcuts="shortcuts"
            :placeholder="t('users.neverExpires')"
            style="width: 100%"
          />
          <NText depth="3" class="hint">{{ t('users.expiresHint') }}</NText>
        </div>
      </NFormItem>
      <div class="row">
        <NFormItem :label="t('users.quota')" class="grow">
          <NInputNumber v-model:value="form.quotaGb" :min="0" :step="10" :placeholder="t('users.unlimited')" style="width: 100%">
            <template #suffix>{{ t('users.quotaUnit') }}</template>
          </NInputNumber>
        </NFormItem>
        <NFormItem v-if="(form.quotaGb ?? 0) > 0" :label="t('users.resetDay')" class="day">
          <NInputNumber v-model:value="form.resetDay" :min="1" :max="28" :precision="0" style="width: 100%" />
        </NFormItem>
      </div>
      <NText depth="3" class="hint quota-hint">
        {{ t('users.quotaHint') }}
        <template v-if="(form.quotaGb ?? 0) > 0">{{ t('users.resetDayHint', { n: form.resetDay ?? 1 }) }}</template>
      </NText>
      <NFormItem v-if="user" :label="t('users.enabled')">
        <NSwitch v-model:value="form.enabled" />
      </NFormItem>
    </NForm>
    <template #footer>
      <NSpace justify="end">
        <NButton :disabled="busy" @click="emit('update:show', false)">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="busy" @click="submit">{{ user ? t('users.save') : t('users.create') }}</NButton>
      </NSpace>
    </template>
  </NModal>
</template>

<style scoped>
.type {
  margin-left: 6px;
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
.quota-hint {
  margin: -16px 0 16px;
}
.row {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
}
.grow {
  flex: 1 1 180px;
}
.day {
  flex: 0 1 150px;
}
.warn {
  display: block;
  font-size: 12px;
  margin: -12px 0 12px;
}
</style>
