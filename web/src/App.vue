<script setup lang="ts">
import { computed, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NConfigProvider, NGlobalStyle, NMessageProvider, NDialogProvider, zhCN, dateZhCN, enUS, dateEnUS } from 'naive-ui'
import type { GlobalThemeOverrides } from 'naive-ui'
import { useThemeMode } from './composables/theme'

const { locale, t, te } = useI18n()
const { theme, isDark } = useThemeMode()
const route = useRoute()

// Browser chrome follows the app: tab title names the page, <html lang>
// the language, and the phone address bar takes the page colour.
watchEffect(() => {
  const name = String(route.name ?? '')
  const page = name === 'login' ? t('login.title') : te(`nav.${name}`) ? t(`nav.${name}`) : ''
  document.title = page ? `${page} · sing-box-web` : 'sing-box-web'
  document.documentElement.lang = locale.value
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', isDark.value ? '#0e1014' : '#f4f5f7')
})

const naiveLocale = computed(() => (locale.value === 'zh-CN' ? zhCN : enUS))
const naiveDateLocale = computed(() => (locale.value === 'zh-CN' ? dateZhCN : dateEnUS))

const font =
  'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei UI", "Microsoft YaHei", sans-serif'

// Three clear surface levels per theme: page < sider/header < card.
const overrides = computed<GlobalThemeOverrides>(() => {
  const dark = isDark.value
  const page = dark ? '#0e1014' : '#f4f5f7'
  const card = dark ? '#171a21' : '#ffffff'
  const border = dark ? 'rgba(255,255,255,0.07)' : 'rgba(15,23,42,0.08)'
  return {
    common: {
      fontFamily: font,
      fontFamilyMono: '"JetBrains Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace',
      fontSize: '14px',
      borderRadius: '8px',
      primaryColor: dark ? '#36ad6a' : '#18a058',
      primaryColorHover: dark ? '#4fc282' : '#36ad6a',
      primaryColorPressed: dark ? '#2a9459' : '#0c7a43',
      bodyColor: page,
      cardColor: card,
      modalColor: card,
      popoverColor: dark ? '#1f232b' : '#ffffff',
      borderColor: border,
      dividerColor: border,
      tableColor: card,
      tableHeaderColor: dark ? '#1c2028' : '#f8f9fb',
    },
    Card: {
      borderRadius: '12px',
      borderColor: border,
      titleFontSizeSmall: '15px',
      titleFontWeight: '600',
      paddingSmall: '16px 20px',
    },
    Layout: {
      color: page,
      siderColor: dark ? '#12141a' : '#ffffff',
      headerColor: dark ? 'rgba(14,16,20,0.85)' : 'rgba(244,245,247,0.85)',
      siderBorderColor: border,
      headerBorderColor: 'transparent',
    },
    Menu: {
      borderRadius: '8px',
      itemHeight: '40px',
      itemColorActive: dark ? 'rgba(54,173,106,0.14)' : 'rgba(24,160,88,0.09)',
      itemColorActiveHover: dark ? 'rgba(54,173,106,0.18)' : 'rgba(24,160,88,0.12)',
      itemIconColor: dark ? '#8b93a1' : '#6b7280',
    },
    DataTable: {
      thFontWeight: '500',
      borderColor: border,
      thColor: dark ? '#1c2028' : '#f8f9fb',
    },
    Tag: { borderRadius: '6px' },
  }
})
</script>

<template>
  <NConfigProvider :theme="theme" :theme-overrides="overrides" :locale="naiveLocale" :date-locale="naiveDateLocale">
    <NGlobalStyle />
    <NMessageProvider>
      <NDialogProvider>
        <RouterView />
      </NDialogProvider>
    </NMessageProvider>
  </NConfigProvider>
</template>
