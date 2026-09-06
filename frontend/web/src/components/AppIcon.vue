<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string, string> = {
  components: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  logout: 'M10 4H4v16h6 M9 12h12 M17 8l4 4-4 4',
  bug: 'M8 8h8v7a4 4 0 0 1-8 0V8z M9 8V6a3 3 0 0 1 6 0v2 M12 9v10 M4 6l4 3 M16 9l4-3 M3 13h5 M16 13h5 M4 20l4-4 M16 16l4 4',
  help: 'M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20 M9.5 9a2.5 2.5 0 1 1 4 2c-1 .6-1.5 1-1.5 3 M12 17v.1',
  'panel-open': 'M3 4h18v16H3z M9 4v16 M13 9l3 3-3 3',
  'panel-close': 'M3 4h18v16H3z M9 4v16 M17 9l-3 3 3 3',
  sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M12 2v2 M12 20v2 M2 12h2 M20 12h2 M5 5l1.5 1.5 M17.5 17.5 19 19 M5 19l1.5-1.5 M17.5 6.5 19 5',
  moon: 'M20 15.5A8.5 8.5 0 0 1 8.5 4 8.5 8.5 0 1 0 20 15.5',
  devices: 'M4 4h16v11H4z M8 20h8 M12 15v5',
  sites: 'M3 21h18 M5 21V7l7-4 7 4v14 M9 9v2 M15 9v2 M9 15v2 M15 15v2',
  topology: 'M9 3h6v6H9z M3 16h6v5H3z M15 16h6v5h-6z M12 9v4 M6 16v-3h12v3',
  search: 'M21 21l-5-5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  arrow: 'M5 12h14 M14 7l5 5-5 5',
  close: 'M6 6l12 12 M18 6 6 18',
  pulse: 'M2 12h5l3-8 4 16 3-8h5',
}
</script>
<template>
  <svg
    width="18"
    height="18"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="1.6"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path :d="paths[name] || paths.devices" />
  </svg>
</template>
