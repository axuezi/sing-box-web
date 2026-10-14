import { createI18n } from 'vue-i18n'
import zhCN from './locales/zh-CN'
import en from './locales/en'
import { readPref } from './composables/prefs'

export type Locale = 'zh-CN' | 'en'

function initialLocale(): Locale {
  const saved = readPref('locale')
  if (saved === 'zh-CN' || saved === 'en') return saved
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

export const i18n = createI18n({
  legacy: false,
  locale: initialLocale(),
  fallbackLocale: 'en',
  messages: { 'zh-CN': zhCN, en },
})
