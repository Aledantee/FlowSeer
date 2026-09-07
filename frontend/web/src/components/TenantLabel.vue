<script setup lang="ts">
import { ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'

const props = defineProps<{ name: string; iconUrl?: string }>()
const failed = ref(false)
watch(
  () => props.iconUrl,
  () => {
    failed.value = false
  },
)
</script>

<template>
  <span class="tenant-label" :title="name" :aria-label="name">
    <span class="tenant-label-badge" aria-hidden="true">
      <img
        v-if="iconUrl && !failed"
        :src="iconUrl"
        alt=""
        class="tenant-label-icon"
        @error="failed = true"
      />
      <AppIcon v-else name="sites" />
    </span>
    <span class="tenant-label-text">{{ name }}</span>
  </span>
</template>
