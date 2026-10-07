<script setup lang="ts">
import { UiAppRoot } from './ui'
import AppFrame from './navigation/AppFrame.vue'
</script>

<template>
  <UiAppRoot>
    <AppFrame>
      <RouterView />
    </AppFrame>
  </UiAppRoot>
</template>
