import { computed, ref, watch } from 'vue'

export type ThemeMode = 'system' | 'dark' | 'light'

const storageKey = 'mirrordock.theme'
const order: ThemeMode[] = ['system', 'dark', 'light']
const labels: Record<ThemeMode, string> = { system: '跟随系统', dark: '深色', light: '浅色' }

function stored(): ThemeMode {
  try {
    const value = localStorage.getItem(storageKey)
    return value === 'dark' || value === 'light' ? value : 'system'
  } catch {
    return 'system'
  }
}

function apply(mode: ThemeMode) {
  const root = document.documentElement
  if (mode === 'system') delete root.dataset.theme
  else root.dataset.theme = mode
}

export const theme = ref<ThemeMode>(stored())
export const themeLabel = computed(() => labels[theme.value])
apply(theme.value)

watch(theme, (mode) => {
  apply(mode)
  try {
    if (mode === 'system') localStorage.removeItem(storageKey)
    else localStorage.setItem(storageKey, mode)
  } catch {
    /* Private browsing may block storage; the choice still applies to this page. */
  }
})

export function cycleTheme() {
  theme.value = order[(order.indexOf(theme.value) + 1) % order.length]!
}
