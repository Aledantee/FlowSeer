<script setup lang="ts">
import { ref } from 'vue'
import AppIcon from './AppIcon.vue'
import { useMotionFeedback } from '../motion/useMotionFeedback'

const dialog = ref<HTMLDialogElement>()
const summary = ref('')
const description = ref('')
const page = ref('')
const feedback = ref('')
const fallback = ref('')
const { play } = useMotionFeedback()
function openReport() {
  page.value = window.location.pathname
  feedback.value = ''
  fallback.value = ''
  dialog.value?.showModal()
  play(dialog.value, {
    opacity: [0.75, 1],
    transform: ['translateY(4px)', 'none'],
  })
}
async function copyReport() {
  const report = `${summary.value.trim()}\n\nPage: ${page.value}\n\n${description.value.trim()}`
  try {
    await navigator.clipboard.writeText(report)
    fallback.value = ''
    feedback.value = 'Report copied. Share it with your support team.'
  } catch {
    fallback.value = report
    feedback.value =
      'Copying was unavailable. Select and copy the report below.'
  }
}
</script>

<template>
  <button
    class="help-button"
    type="button"
    aria-label="Report bug"
    title="Report bug"
    @click="openReport"
  >
    <AppIcon name="bug" />
  </button>
  <dialog ref="dialog" class="help-dialog" aria-labelledby="report-bug-title">
    <div class="help-heading">
      <h2 id="report-bug-title">Report a bug</h2>
      <button
        class="icon-button"
        aria-label="Close bug report"
        @click="dialog?.close()"
      >
        <AppIcon name="close" />
      </button>
    </div>
    <p>
      Describe the issue, then copy the report to share with your support team.
    </p>
    <form class="bug-report-form" @submit.prevent="copyReport">
      <label for="bug-summary">Summary</label>
      <input id="bug-summary" v-model="summary" required maxlength="200" />
      <label for="bug-description">What happened?</label>
      <textarea
        id="bug-description"
        v-model="description"
        required
        maxlength="5000"
        rows="5"
        placeholder="Steps to reproduce and what you expected to happen"
      ></textarea>
      <p>Page: {{ page }}</p>
      <button class="primary" type="submit">Copy report</button>
      <p role="status">{{ feedback }}</p>
      <template v-if="fallback">
        <label for="bug-report-copy">Report text</label>
        <textarea
          id="bug-report-copy"
          :value="fallback"
          readonly
          rows="6"
        ></textarea>
      </template>
    </form>
  </dialog>
</template>
