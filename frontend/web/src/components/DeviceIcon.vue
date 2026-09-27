<script setup lang="ts">
import type { DeviceRole } from '../domain/fleet'
import AppIcon from './AppIcon.vue'
defineProps<{ role: DeviceRole }>()
</script>

<template>
  <span
    class="device-icon grid place-items-center w-[31px] h-8 bg-subtle border border-border rounded-control text-muted-foreground shrink-0 [&>svg]:w-4 [&>svg]:h-4"
  >
    <AppIcon :name="role" />
  </span>
</template>
