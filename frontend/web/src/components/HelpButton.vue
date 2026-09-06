<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { useMotionFeedback } from '../motion/useMotionFeedback'

const dialog = ref<HTMLDialogElement>()
const { play } = useMotionFeedback()
function openHelp() {
  dialog.value?.showModal()
  play(dialog.value, {
    opacity: [0.75, 1],
    transform: ['translateY(4px)', 'none'],
  })
}
</script>

<template>
  <button
    class="help-button"
    type="button"
    aria-label="Help"
    title="Help"
    @click="openHelp"
  >
    <AppIcon name="help" />
  </button>
  <dialog ref="dialog" class="help-dialog" aria-labelledby="help-title">
    <div class="help-heading">
      <h2 id="help-title">Workspace help</h2>
      <button
        class="icon-button"
        aria-label="Close help"
        @click="dialog?.close()"
      >
        <AppIcon name="close" />
      </button>
    </div>
    <h3>Choose your scope</h3>
    <p>
      Use the tenant selector in the sidebar and the site selector in the
      breadcrumb to focus on a customer or location.
    </p>
    <h3>Find a device</h3>
    <p>
      Search by name, type, or IP address. Filter by status to find devices
      needing attention, then select a device to open its details.
    </p>
    <h3>Move a device</h3>
    <p>
      Open device details and choose a site within its tenant. Saving replaces
      the device’s previous site assignment.
    </p>
  </dialog>
</template>
