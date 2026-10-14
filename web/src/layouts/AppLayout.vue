<script setup lang="ts">
import { computed, h, ref, type Component } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { NButton, NDrawer, NDrawerContent, NDropdown, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu, NTooltip } from 'naive-ui'
import type { MenuOption } from 'naive-ui'
import {
  BarChart3, Languages, LayoutDashboard, LogOut, Menu, Monitor, Moon, Network, Settings, Sun, Users,
} from 'lucide-vue-next'
import { navItems } from '../router'
import { useThemeMode, type ThemeMode } from '../composables/theme'
import { writePref } from '../composables/prefs'
import { useIsMobile } from '../composables/viewport'
import { logout, me } from '../composables/session'
import type { Locale } from '../i18n'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()
const { mode } = useThemeMode()
const isMobile = useIsMobile()
const drawerOpen = ref(false)

const icons: Record<string, Component> = {
  overview: LayoutDashboard,
  users: Users,
  traffic: BarChart3,
  nodes: Network,
  settings: Settings,
}
const icon = (c: Component) => () => h(c, { size: 18, strokeWidth: 1.75 })

const menuOptions = computed<MenuOption[]>(() =>
  navItems.map((item) => ({ key: item.name, label: t(`nav.${item.name}`), icon: icon(icons[item.name]!) })),
)
const active = computed(() => String(route.name ?? 'overview'))

function go(key: string) {
  router.push({ name: key })
  drawerOpen.value = false
}

// Theme: one button cycling system → light → dark.
const themeOrder: ThemeMode[] = ['auto', 'light', 'dark']
const themeIcon = computed(() => ({ auto: Monitor, light: Sun, dark: Moon })[mode.value])
function cycleTheme() {
  mode.value = themeOrder[(themeOrder.indexOf(mode.value) + 1) % themeOrder.length]!
}

const localeOptions = [
  { key: 'zh-CN', label: '中文' },
  { key: 'en', label: 'English' },
]
function setLocale(value: Locale) {
  locale.value = value
  writePref('locale', value)
  document.documentElement.lang = value
}

const userOptions = computed(() => [
  { key: 'settings', label: t('nav.settings'), icon: icon(Settings) },
  { type: 'divider', key: 'd' },
  { key: 'logout', label: t('user.logout'), icon: icon(LogOut) },
])
async function onUser(key: string) {
  if (key === 'settings') return go('settings')
  await logout()
  router.replace({ name: 'login' })
}
const initial = computed(() => (me.value?.username ?? '?').slice(0, 1).toUpperCase())
</script>

<template>
  <NLayout :has-sider="!isMobile" class="shell">
    <NLayoutSider v-if="!isMobile" bordered :width="220" :native-scrollbar="false">
      <div class="brand">
        <img src="/favicon.svg" alt="" width="28" height="28" />
        <div class="brand-text">
          <div class="brand-name">sing-box-web</div>
          <div class="brand-sub">{{ t('login.subtitle') }}</div>
        </div>
      </div>
      <NMenu :options="menuOptions" :value="active" :indent="16" class="menu" @update:value="go" />
    </NLayoutSider>
    <NDrawer v-else v-model:show="drawerOpen" placement="left" :width="248">
      <NDrawerContent :body-content-style="{ padding: 0 }">
        <div class="brand">
          <img src="/favicon.svg" alt="" width="28" height="28" />
          <div class="brand-text">
            <div class="brand-name">sing-box-web</div>
            <div class="brand-sub">{{ t('login.subtitle') }}</div>
          </div>
        </div>
        <NMenu :options="menuOptions" :value="active" :indent="16" class="menu" @update:value="go" />
      </NDrawerContent>
    </NDrawer>

    <NLayout class="main" :native-scrollbar="false">
      <NLayoutHeader class="header">
        <div class="header-inner">
          <div class="header-left">
            <NButton v-if="isMobile" quaternary circle aria-label="menu" @click="drawerOpen = true">
              <template #icon><Menu :size="20" /></template>
            </NButton>
            <h1 class="title">{{ t(`nav.${active}`) }}</h1>
          </div>
          <div class="header-right">
            <NTooltip>
              <template #trigger>
                <NButton quaternary circle :aria-label="t(`theme.${mode}`)" @click="cycleTheme">
                  <template #icon><component :is="themeIcon" :size="18" :stroke-width="1.75" /></template>
                </NButton>
              </template>
              {{ t(`theme.${mode}`) }}
            </NTooltip>
            <NDropdown :options="localeOptions" trigger="click" :value="locale" @select="setLocale">
              <NButton quaternary circle aria-label="language">
                <template #icon><Languages :size="18" :stroke-width="1.75" /></template>
              </NButton>
            </NDropdown>
            <NDropdown :options="userOptions" trigger="click" placement="bottom-end" @select="onUser">
              <button class="user" type="button">
                <span class="avatar">{{ initial }}</span>
                <span v-if="!isMobile" class="uname">{{ me?.username }}</span>
              </button>
            </NDropdown>
          </div>
        </div>
      </NLayoutHeader>
      <NLayoutContent class="content-wrap">
        <div class="content">
          <RouterView />
        </div>
      </NLayoutContent>
    </NLayout>
  </NLayout>
</template>

<style scoped>
.shell {
  height: 100vh;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 64px;
  padding: 0 20px;
}
.brand-text {
  line-height: 1.2;
}
.brand-name {
  font-weight: 650;
  font-size: 15px;
  letter-spacing: -0.01em;
}
.brand-sub {
  font-size: 11.5px;
  opacity: 0.55;
}
.menu {
  padding: 4px 8px;
}
.header {
  position: sticky;
  top: 0;
  z-index: 10;
  backdrop-filter: saturate(160%) blur(8px);
}
.header-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  height: 60px;
  padding: 0 28px;
  max-width: 1480px;
  margin: 0 auto;
  box-sizing: border-box;
}
.header-left,
.header-right {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}
.title {
  margin: 0;
  font-size: 20px;
  font-weight: 650;
  letter-spacing: -0.01em;
}
.user {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  margin-left: 6px;
  padding: 4px 10px 4px 4px;
  border: 0;
  border-radius: 999px;
  background: transparent;
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.user:hover {
  background: rgba(127, 127, 127, 0.12);
}
.avatar {
  display: inline-grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: #18a058;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
}
.uname {
  font-size: 13px;
}
.content {
  max-width: 1480px;
  margin: 0 auto;
  padding: 8px 28px 32px;
  box-sizing: border-box;
}
@media (max-width: 767px) {
  .header-inner {
    padding: 0 12px;
  }
  .title {
    font-size: 17px;
  }
  .content {
    padding: 4px 12px 24px;
  }
}
</style>
