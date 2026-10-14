<script setup lang="ts">
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NTag, NText } from 'naive-ui'
import type { DataTableColumns } from 'naive-ui'
import { api, type AuditEntry } from '../../api'
import { formatDateTime } from '../../composables/format'
import { auditTagType, useAuditDetail, useAuditLabel } from '../../composables/audit'
import { useIsMobile } from '../../composables/viewport'
import AuditRow from '../../components/AuditRow.vue'

const PAGE = 100
const { t } = useI18n()
const label = useAuditLabel()
const detail = useAuditDetail()
const isMobile = useIsMobile()

const entries = ref<AuditEntry[]>([])
const loading = ref(false)
const done = ref(false)

async function more() {
  loading.value = true
  try {
    const before = entries.value.at(-1)?.id ?? 0
    const res = await api<{ entries: AuditEntry[] }>(`/audit?limit=${PAGE}${before ? `&before=${before}` : ''}`)
    entries.value = entries.value.concat(res.entries)
    done.value = res.entries.length < PAGE
  } finally {
    loading.value = false
  }
}

const columns = computed<DataTableColumns<AuditEntry>>(() => [
  { title: t('logs.time'), key: 'at', width: 180, render: (r) => formatDateTime(r.at) },
  {
    title: t('logs.action'),
    key: 'action',
    width: 150,
    render: (r) => h(NTag, { size: 'small', type: auditTagType(r.action), bordered: false }, () => label(r.action)),
  },
  { title: t('logs.actor'), key: 'actor', width: 120 },
  { title: t('logs.ip'), key: 'ip', width: 150, render: (r) => h(NText, { class: 'mono' }, () => r.ip) },
  { title: t('logs.target'), key: 'target', ellipsis: { tooltip: true } },
  { title: t('logs.detail'), key: 'detail', ellipsis: { tooltip: true }, render: (r) => detail(r.detail) },
])

onMounted(more)
</script>

<template>
  <div>
    <div v-if="isMobile">
      <AuditRow v-for="e in entries" :key="e.id" :entry="e" />
    </div>
    <NDataTable v-else :columns="columns" :data="entries" :loading="loading" size="small" :bordered="false" :scroll-x="820" />
    <div class="more">
      <NButton v-if="!done" size="small" :loading="loading" @click="more">{{ t('common.loadMore') }}</NButton>
      <NText v-else depth="3">{{ t('common.noMore') }}</NText>
    </div>
  </div>
</template>

<style scoped>
.more {
  display: flex;
  justify-content: center;
  padding: 12px 0;
  font-size: 12px;
}
:deep(.mono) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
