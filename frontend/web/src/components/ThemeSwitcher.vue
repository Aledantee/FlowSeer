<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useMotionFeedback } from '../motion/useMotionFeedback'
import AppIcon from './AppIcon.vue'

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
const announcement = ref('')
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
    announcement.value = `${theme.value === 'dark' ? 'Dark' : 'Light'} mode enabled.`
  } catch {
    announcement.value =
      'Theme changed for this page. Your browser could not save the preference.'
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
        transform: dark
          ? ['rotate(0deg) scale(1)', 'rotate(45deg) scale(0.65)']
          : ['rotate(-45deg) scale(0.65)', 'rotate(0deg) scale(1)'],
      },
      0.16,
    )
    play(
      moon.value,
      {
        opacity: dark ? [0, 1] : [1, 0],
        transform: dark
          ? ['rotate(-35deg) scale(0.65)', 'rotate(0deg) scale(1)']
          : ['rotate(0deg) scale(1)', 'rotate(35deg) scale(0.65)'],
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
  <button
    class="theme-switcher"
    type="button"
    role="switch"
    aria-label="Dark mode"
    :aria-checked="theme === 'dark'"
    :title="`Switch to ${theme === 'dark' ? 'light' : 'dark'} mode`"
    @click="toggleTheme"
  >
    <span
      ref="sun"
      class="theme-icon"
      :class="{ 'is-active': theme === 'light' }"
      aria-hidden="true"
      ><AppIcon name="sun"
    /></span>
    <span
      ref="moon"
      class="theme-icon"
      :class="{ 'is-active': theme === 'dark' }"
      aria-hidden="true"
      ><AppIcon name="moon"
    /></span>
  </button>
  <span class="sr-only" role="status">{{ announcement }}</span>
</template>
