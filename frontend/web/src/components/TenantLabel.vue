<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import AppIcon from './AppIcon.vue'
const props = defineProps<{
  name: string
  iconUrl?: string
  scoped?: boolean
}>()
const failed = ref(false)
// A single tenant reduces to its first letter; "all tenants" has no letter that
// stands for it, so it keeps the layers glyph instead.
const initial = computed(() => props.name.trim().charAt(0).toUpperCase())
watch(
  () => props.iconUrl,
  () => (failed.value = false),
)
</script>

<template>
  <span class="tenant-label" :title="name" :aria-label="name">
    <span class="tenant-label-mark" aria-hidden="true">
      <img
        v-if="iconUrl && !failed"
        :src="iconUrl"
        alt=""
        @error="failed = true"
      />
      <AppIcon v-else-if="!scoped" name="tenants" />
      <span v-else class="tenant-label-initial">{{ initial }}</span>
    </span>
    <span class="tenant-label-text">{{ name }}</span>
  </span>
</template>
