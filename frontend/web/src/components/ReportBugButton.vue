<script setup lang="ts">
import { ref } from 'vue'
import {
  UiButton,
  UiDialog,
  UiField,
  UiInput,
  UiTextarea,
  UiTooltip,
} from '../ui'
import AppIcon from './AppIcon.vue'

const open = ref(false)
const summary = ref('')
const description = ref('')
const page = ref('')
const feedback = ref('')
const fallback = ref('')

function openReport() {
  page.value = window.location.pathname
  feedback.value = ''
  fallback.value = ''
  open.value = true
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
  <UiDialog v-model:open="open" title="Report a bug">
    <template #trigger>
      <UiTooltip label="Report a bug">
        <button
          class="help-button"
          type="button"
          aria-label="Report bug"
          @click="openReport"
        >
          <AppIcon name="bug" />
        </button>
      </UiTooltip>
    </template>

    <p class="text-sm text-muted-foreground mb-4">
      Describe the issue, then copy the report to share with your support team.
    </p>

    <form class="flex flex-col gap-4" @submit.prevent="copyReport">
      <UiField id="bug-summary" label="Summary" required>
        <UiInput id="bug-summary" v-model="summary" required maxlength="200" />
      </UiField>

      <UiField id="bug-description" label="What happened?" required>
        <UiTextarea
          id="bug-description"
          v-model="description"
          required
          maxlength="5000"
          :rows="5"
          placeholder="Steps to reproduce and what you expected to happen"
        />
      </UiField>

      <p class="text-xs text-muted-foreground">Page: {{ page }}</p>

      <UiButton type="submit" variant="primary">Copy report</UiButton>

      <p v-if="feedback" role="status" class="text-xs text-foreground">
        {{ feedback }}
      </p>

      <template v-if="fallback">
        <UiField id="bug-report-copy" label="Report text">
          <UiTextarea
            id="bug-report-copy"
            :model-value="fallback"
            readonly
            :rows="6"
          />
        </UiField>
      </template>
    </form>
  </UiDialog>
</template>
