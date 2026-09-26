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
  dashboard: 'M3 3h8v10H3z M13 3h8v6h-8z M13 11h8v10h-8z M3 15h8v6H3z',
  devices: 'M4 4h16v11H4z M8 20h8 M12 15v5',
  sites: 'M3 21h18 M5 21V7l7-4 7 4v14 M9 9v2 M15 9v2 M9 15v2 M15 15v2',
  topology: 'M9 3h6v6H9z M3 16h6v5H3z M15 16h6v5h-6z M12 9v4 M6 16v-3h12v3',
  search: 'M21 21l-5-5 M18 10a8 8 0 1 1-16 0 8 8 0 0 1 16 0',
  arrow: 'M5 12h14 M14 7l5 5-5 5',
  close: 'M6 6l12 12 M18 6 6 18',
  pulse: 'M2 12h5l3-8 4 16 3-8h5',
  tenants: 'M12 3 3 7.5 12 12l9-4.5L12 3 M3 12l9 4.5L21 12 M3 16.5 12 21l9-4.5',
  gateway:
    'M3 14h18v6H3z M7 17h.01 M11 17h.01 M8.5 10.5a5 5 0 0 1 7 0 M5.5 7.5a9.5 9.5 0 0 1 13 0',
  switch: 'M3 8h18v8H3z M7 12h.01 M10 12h.01 M13 12h.01 M16 12h.01',
  'access-point':
    'M2 9a14 14 0 0 1 20 0 M5 12.5a9.5 9.5 0 0 1 14 0 M8.5 16a5 5 0 0 1 7 0 M12 19.5v.01',
  clients:
    'M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M2 21v-1a6 6 0 0 1 12 0v1 M16 3.5a4 4 0 0 1 0 7.5 M22 21v-1a6 6 0 0 0-4-5.6',
  chevron: 'M6 9l6 6 6-6',
  check: 'M5 12.5l4.5 4.5L19 7',
  back: 'M19 12H5 M10 7l-5 5 5 5',
  'to-dock': 'M4 3h16v11H4z M12 6v5 M9.5 8.5 12 11l2.5-2.5 M7 20h10',
  split: 'M3 4h18v16H3z M12 4v16',
  'panel-right': 'M3 4h18v16H3z M14 4v16',
  'dock-pair': 'M3 3h18v11H3z M12 3v11 M8 20h8 M12 16v4',
  swap: 'M7 4 3 8l4 4 M3 8h14 M17 20l4-4-4-4 M21 16H7',
  link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1 M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
  'link-clicks': 'M3 4h18v16H3z M12 4v16 M5 12h10 M12 9l3 3-3 3',
  expand: 'M4 9V4h5 M20 9V4h-5 M4 15v5h5 M20 15v5h-5',
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
