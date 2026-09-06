<script setup lang="ts">
import AppIcon from './AppIcon.vue'
defineProps<{ label: string; value: number; unit?: string; icon: string }>()
</script>

<template>
  <article class="metric-card">
    <span class="metric-label">{{ label }}<AppIcon :name="icon" /></span>
    <div class="metric-number">
      {{ value }}<span v-if="unit" class="metric-unit">{{ unit }}</span>
    </div>
    <span class="metric-note"><slot /></span>
  </article>
</template>
