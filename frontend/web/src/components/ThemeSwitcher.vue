<script setup lang="ts">
import { UiTooltip, useMotionFeedback } from '../ui'
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIcon from './AppIcon.vue'

const { t } = useI18n({ useScope: 'global' })
const { play } = useMotionFeedback()
const sun = ref<HTMLElement>()
const moon = ref<HTMLElement>()

type Theme = 'light' | 'dark'
const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
let savedTheme: string | null = null
try {
  savedTheme = localStorage.getItem('flowseer.theme')
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
    localStorage.setItem('flowseer.theme', theme.value)
    changed.value = theme.value
  } catch {
    changed.value = 'unsaved'
  }
}
watch(
  theme,
  (value) => {
    const dark = value === 'dark'
    play(
      sun.value,
      {
        opacity: dark ? [1, 0] : [0, 1],
        rotate: dark ? [0, 45] : [-45, 0],
        scale: dark ? [1, 0.65] : [0.65, 1],
      },
      0.16,
    )
    play(
      moon.value,
      {
        opacity: dark ? [0, 1] : [1, 0],
        rotate: dark ? [-35, 0] : [0, 35],
        scale: dark ? [0.65, 1] : [1, 0.65],
      },
      0.16,
    )
  },
  { flush: 'post' },
)
function syncSystemTheme(event: MediaQueryListEvent) {
  if (followsSystem) applyTheme(event.matches ? 'dark' : 'light')
}
onMounted(() => systemTheme.addEventListener('change', syncSystemTheme))
onUnmounted(() => systemTheme.removeEventListener('change', syncSystemTheme))
</script>

<template>
  <UiTooltip
    :label="
      theme === 'dark'
        ? t('view.themeSwitcher.switchToLight')
        : t('view.themeSwitcher.switchToDark')
    "
  >
    <button
      class="theme-switcher relative grid place-items-center w-11 h-11 p-0 bg-transparent text-chrome-foreground border-0 rounded hover:bg-chrome-hover cursor-pointer"
      type="button"
      role="switch"
      :aria-label="t('view.themeSwitcher.label')"
      :aria-checked="theme === 'dark'"
      @click="toggleTheme"
    >
      <span
        ref="sun"
        class="theme-icon absolute inset-0 grid place-items-center opacity-0 pointer-events-none [&>svg]:w-5 [&>svg]:h-5"
        :class="{ 'is-active opacity-100': theme === 'light' }"
        aria-hidden="true"
        ><AppIcon name="sun"
      /></span>
      <span
        ref="moon"
        class="theme-icon absolute inset-0 grid place-items-center opacity-0 pointer-events-none [&>svg]:w-5 [&>svg]:h-5"
        :class="{ 'is-active opacity-100': theme === 'dark' }"
        aria-hidden="true"
        ><AppIcon name="moon"
      /></span>
    </button>
  </UiTooltip>
  <span class="sr-only" role="status">{{ announcement }}</span>
</template>
