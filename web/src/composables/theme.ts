import { computed, ref, watch, watchEffect } from 'vue'
import { darkTheme, useOsTheme } from 'naive-ui'
import { readPref, writePref } from './prefs'

export type ThemeMode = 'auto' | 'light' | 'dark'

const saved = readPref('theme')
const mode = ref<ThemeMode>(saved === 'light' || saved === 'dark' ? saved : 'auto')
watch(mode, (m) => writePref('theme', m))

export function useThemeMode() {
  const os = useOsTheme()
  const isDark = computed(() => (mode.value === 'auto' ? os.value === 'dark' : mode.value === 'dark'))
  const theme = computed(() => (isDark.value ? darkTheme : null))
  // Lets plain CSS (chart tokens) follow the same switch as Naive UI.
  watchEffect(() => (document.documentElement.dataset.theme = isDark.value ? 'dark' : 'light'))
  return { mode, isDark, theme }
}
