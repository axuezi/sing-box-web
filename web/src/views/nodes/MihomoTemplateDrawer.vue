<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NDrawer, NDrawerContent, NInput, NSpace, NTag, NText, useMessage } from 'naive-ui'
import { api, put } from '../../api'
import { useApplyFeedback } from '../../composables/applyFeedback'
import { useIsMobile } from '../../composables/viewport'

// Edits the Mihomo (Clash) profile users get from their subscription: the
// panel fills in {{proxies}} and {{proxy_names}}, everything else is the
// template's. Saving the built-in text unchanged keeps following it.
const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{ 'update:show': [boolean]; saved: [] }>()
const { t } = useI18n()
const message = useMessage()
const feedback = useApplyFeedback()
const isMobile = useIsMobile()

type SubSettings = { public_host: string; mihomo_template?: string; [k: string]: unknown }
const text = ref('')
const builtIn = ref('')
const stored = ref('')
const busy = ref(false)
// Placeholders go in as parameters: vue-i18n would parse literal braces.
const ph = { p: '{{proxies}}', n: '{{proxy_names}}' }
const loading = ref(false)

const isDefault = computed(() => text.value.replace(/\r\n/g, '\n') === builtIn.value)
const dirty = computed(() => text.value !== (stored.value || builtIn.value))
const hasProxies = computed(() => text.value.split('\n').some((l) => l.trim() === '{{proxies}}'))

watch(
  () => props.show,
  async (open) => {
    if (!open) return
    loading.value = true
    try {
      const r = await api<{ settings: SubSettings; mihomo_default_template: string }>('/settings/subscription')
      builtIn.value = r.mihomo_default_template
      stored.value = r.settings.mihomo_template ?? ''
      text.value = stored.value || builtIn.value
    } catch (e) {
      feedback.failure(e)
    } finally {
      loading.value = false
    }
  },
)

async function save() {
  busy.value = true
  try {
    const cur = await api<{ settings: SubSettings }>('/settings/subscription')
    await put('/settings/subscription', { ...cur.settings, mihomo_template: isDefault.value ? '' : text.value })
    message.success(t('mihomoTpl.saved'))
    emit('saved')
    emit('update:show', false)
  } catch (e) {
    feedback.failure(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NDrawer :show="show" :width="isMobile ? '100%' : 720" placement="right" :mask-closable="!busy" @update:show="emit('update:show', $event)">
    <NDrawerContent :title="t('mihomoTpl.title')" closable>
      <NSpace vertical :size="12">
        <NSpace align="center" :size="8">
          <NTag size="small" :bordered="false" :type="isDefault ? 'default' : 'success'">
            {{ isDefault ? t('mihomoTpl.builtIn') : t('mihomoTpl.custom') }}
          </NTag>
          <NText depth="3" class="sbw-tpl-hint">{{ t('mihomoTpl.scope') }}</NText>
        </NSpace>
        <NAlert type="info" :show-icon="false" class="sbw-tpl-help">
          <div>{{ t('mihomoTpl.helpProxies', ph) }}</div>
          <div>{{ t('mihomoTpl.helpNames', ph) }}</div>
          <div>{{ t('mihomoTpl.helpYaml') }}</div>
        </NAlert>
        <NAlert v-if="!loading && !hasProxies" type="warning" :show-icon="false">{{ t('mihomoTpl.missingProxies', ph) }}</NAlert>
        <NInput
          v-model:value="text"
          type="textarea"
          class="sbw-yaml-editor"
          :autosize="{ minRows: 18, maxRows: 40 }"
          :loading="loading"
          :disabled="loading"
          :input-props="{ spellcheck: false, autocapitalize: 'off', autocomplete: 'off' }"
        />
      </NSpace>
      <template #footer>
        <NSpace justify="space-between" style="width: 100%">
          <NButton :disabled="busy || isDefault" @click="text = builtIn">{{ t('mihomoTpl.restore') }}</NButton>
          <NSpace>
            <NButton :disabled="busy" @click="emit('update:show', false)">{{ t('common.cancel') }}</NButton>
            <NButton type="primary" :loading="busy" :disabled="!dirty || !hasProxies" @click="save">{{ t('common.save') }}</NButton>
          </NSpace>
        </NSpace>
      </template>
    </NDrawerContent>
  </NDrawer>
</template>

<style>
.sbw-tpl-hint {
  font-size: 12px;
}
.sbw-tpl-help {
  font-size: 12px;
  line-height: 1.7;
}
/* Not scoped: the drawer is teleported to <body>. YAML must not soft-wrap,
   or indentation becomes hard to read; Naive UI's own textarea rules
   (pre-wrap, break-word) win without !important. */
.sbw-yaml-editor textarea,
.sbw-yaml-editor .n-input__textarea-mirror {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, 'Liberation Mono', monospace !important;
  font-size: 12px !important;
  line-height: 1.5 !important;
  white-space: pre !important;
  overflow-wrap: normal !important;
  word-break: normal !important;
}
.sbw-yaml-editor textarea {
  overflow-x: auto !important;
}
</style>
