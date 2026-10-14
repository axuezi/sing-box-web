<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NTabPane, NTabs } from 'naive-ui'
import InboundsTab from './InboundsTab.vue'
import OutboundsTab from './OutboundsTab.vue'
import DnsTab from './DnsTab.vue'

// Everything that edits sing-box's config.json, one tab per section.
type Tab = 'inbounds' | 'outbounds' | 'dns'
const TABS: Tab[] = ['inbounds', 'outbounds', 'dns']

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const tab = ref<Tab>(TABS.includes(route.query.tab as Tab) ? (route.query.tab as Tab) : 'inbounds')
watch(tab, (v) => router.replace({ query: v === 'inbounds' ? {} : { tab: v } }))
</script>

<template>
  <NTabs v-model:value="tab" type="line" animated class="tabs">
    <NTabPane name="inbounds" :tab="t('nodes.inbounds')" display-directive="show:lazy">
      <InboundsTab />
    </NTabPane>
    <NTabPane name="outbounds" :tab="t('nodes.outbounds')" display-directive="show:lazy">
      <OutboundsTab />
    </NTabPane>
    <NTabPane name="dns" tab="DNS" display-directive="show:lazy">
      <DnsTab />
    </NTabPane>
  </NTabs>
</template>

<style scoped>
.tabs :deep(.n-tabs-nav) {
  margin-bottom: 4px;
}
</style>
