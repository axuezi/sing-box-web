<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NCard, NTabPane, NTabs } from 'naive-ui'
import LiveLog from './LiveLog.vue'
import AuditTable from './AuditTable.vue'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
// Inside the settings page, whose own tab is ?tab=; this one is ?log=.
const tab = ref(route.query.log === 'audit' ? 'audit' : 'live')
watch(tab, (v) => router.replace({ query: { ...route.query, log: v === 'audit' ? 'audit' : undefined } }))
</script>

<template>
  <NCard size="small" class="card" content-class="card-content">
    <NTabs v-model:value="tab" type="line" animated class="tabs">
      <NTabPane name="live" :tab="t('logs.live')" display-directive="show:lazy" class="pane">
        <LiveLog />
      </NTabPane>
      <NTabPane name="audit" :tab="t('logs.audit')" display-directive="show:lazy">
        <AuditTable />
      </NTabPane>
    </NTabs>
  </NCard>
</template>

<style scoped>
.pane {
  height: calc(100vh - 250px);
  min-height: 380px;
}
</style>
