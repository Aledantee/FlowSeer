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
          Use the tenant and site selectors in the breadcrumb at the top of the
          page to focus on a customer or location.
        </p>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">Find a device</h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          Devices that need attention are listed first. Search by name, type, or
          IP address, or filter by status, then select a device to see why it
          needs attention and how FlowSeer reaches it.
        </p>
      </div>
      <div>
        <h3 class="font-medium text-foreground mb-1">Move a device</h3>
        <p class="text-muted-foreground text-xs leading-relaxed">
          Open device details, expand Move to another site, and choose a site
          within its tenant. The move replaces the previous assignment once it
          is observed, and the notice offers Undo.
        </p>
      </div>
    </div>
  </UiDialog>
</template>
