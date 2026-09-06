<script setup lang="ts">
defineProps<{ status: 'Healthy' | 'Degraded' | 'Offline' }>()
</script>

<template>
  <span :class="['status', status.toLowerCase()]"
    ><i aria-hidden="true"></i>{{ status }}</span
  >
</template>
