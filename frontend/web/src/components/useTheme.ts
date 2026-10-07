import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

export type Theme = 'light' | 'dark'

const STORAGE_KEY = 'flowseer.theme'

// The saved theme wins, then the system's. The caller's component applies it
// to the document for as long as it is mounted and follows the system until
// the visitor chooses.
export function useTheme() {
  const { t } = useI18n({ useScope: 'global' })
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
  let savedTheme: string | null = null
  try {
    savedTheme = localStorage.getItem(STORAGE_KEY)
  } catch {
    // Browser storage can be unavailable in restricted browsing contexts.
  }
  let followsSystem = savedTheme !== 'light' && savedTheme !== 'dark'
  const theme = ref<Theme>(
    savedTheme === 'light' || savedTheme === 'dark'
      ? savedTheme
      : systemTheme.matches
        ? 'dark'
        : 'light',
  )
  // The last change as a state, not as text, so the status follows a locale
  // switch.
  const changed = ref<Theme | 'unsaved' | ''>('')
  const announcement = computed(() =>
    changed.value === 'dark'
      ? t('view.themeSwitcher.enabledDark')
      : changed.value === 'light'
        ? t('view.themeSwitcher.enabledLight')
        : changed.value === 'unsaved'
          ? t('view.themeSwitcher.unsaved')
          : '',
  )
  function applyTheme(value: Theme) {
    theme.value = value
    document.documentElement.dataset.theme = value
  }
  applyTheme(theme.value)
  function toggleTheme() {
    followsSystem = false
    applyTheme(theme.value === 'dark' ? 'light' : 'dark')
    try {
      localStorage.setItem(STORAGE_KEY, theme.value)
      changed.value = theme.value
    } catch {
      changed.value = 'unsaved'
    }
  }
  function syncSystemTheme(event: MediaQueryListEvent) {
    if (followsSystem) applyTheme(event.matches ? 'dark' : 'light')
  }
  onMounted(() => systemTheme.addEventListener('change', syncSystemTheme))
  onUnmounted(() => systemTheme.removeEventListener('change', syncSystemTheme))

  return { theme, announcement, toggleTheme }
}
