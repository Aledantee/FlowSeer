<script setup lang="ts">
import { ref } from 'vue'
import { UiDialog, UiTooltip } from '../ui'
import AppIcon from './AppIcon.vue'

const open = ref(false)
</script>

<template>
  <UiDialog v-model:open="open" title="Workspace help">
    <template #trigger>
      <UiTooltip label="Help">
        <button
          class="help-button grid place-items-center w-11 h-11 p-0 bg-transparent border-0 text-chrome-foreground rounded hover:bg-chrome-hover cursor-pointer [&>svg]:w-5 [&>svg]:h-5"
          type="button"
          aria-label="Help"
        >
          <AppIcon name="help" />
        </button>
      </UiTooltip>
    </template>
    <div class="space-y-4 text-sm text-foreground">
      <div>
        <h3 class="font-medium text-foreground mb-1">Choose your scope</h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          Use the tenant selector in the sidebar and the site selector in the
          breadcrumb to focus on a customer or location.
        </p>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">Find a device</h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          Search by name, type, or IP address. Filter by status to find devices
          needing attention, then select a device to open its details.
        </p>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">Move a device</h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          Open device details and choose a site within its tenant. Saving
          replaces the device’s previous site assignment.
        </p>
      </div>
    </div>
  </UiDialog>
</template>
