<script setup lang="ts">
import type { DeviceRole } from '../domain/fleet'
import AppIcon from './AppIcon.vue'
defineProps<{ role: DeviceRole }>()
</script>

<template>
  <span class="device-icon"><AppIcon :name="role" /></span>
</template>
