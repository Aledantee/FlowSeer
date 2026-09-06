<script setup lang="ts">
defineProps<{ name: string }>()
const paths: Record<string, string> = {
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
