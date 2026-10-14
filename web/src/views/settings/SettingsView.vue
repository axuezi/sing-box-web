<script setup lang="ts">
import { ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NTabPane, NTabs } from 'naive-ui'
import AccountTab from './AccountTab.vue'
import AccessTab from './AccessTab.vue'
import LogsTab from './LogsTab.vue'

// The admin's own account, who may reach the proxy, and what happened.
type Tab = 'account' | 'access' | 'logs'
const TABS: Tab[] = ['account', 'access', 'logs']

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const tab = ref<Tab>(TABS.includes(route.query.tab as Tab) ? (route.query.tab as Tab) : 'account')
watch(tab, (v) => router.replace({ query: v === 'account' ? {} : { tab: v } }))
</script>

<template>
  <NTabs v-model:value="tab" type="line" animated class="tabs">
    <NTabPane name="account" :tab="t('settings.tabs.account')" display-directive="show:lazy">
      <AccountTab />
    </NTabPane>
    <NTabPane name="access" :tab="t('nav.access')" display-directive="show:lazy">
      <AccessTab />
    </NTabPane>
    <NTabPane name="logs" :tab="t('nav.logs')" display-directive="show:lazy">
      <LogsTab />
    </NTabPane>
  </NTabs>
</template>

<style scoped>
.tabs :deep(.n-tabs-nav) {
  margin-bottom: 4px;
}
</style>
