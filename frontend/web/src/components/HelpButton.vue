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
      Use the tenant and site selectors in the breadcrumb at the top of the page
      to focus on a customer or location.
    </p>
    <h3>Find a device</h3>
    <p>
      Devices that need attention are listed first. Search by name, type, or IP
      address, or filter by status, then select a device to see why it needs
      attention and how FlowSeer reaches it.
    </p>
    <h3>Move a device</h3>
    <p>
      Open device details, expand Move to another site, and choose a site within
      its tenant. The move replaces the previous assignment once it is observed,
      and the notice offers Undo.
    </p>
  </dialog>
</template>
