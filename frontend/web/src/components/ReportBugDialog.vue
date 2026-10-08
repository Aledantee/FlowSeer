<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { UiButton, UiDialog, UiField, UiInput, UiTextarea } from '../ui'

const { t } = useI18n({ useScope: 'global' })
const open = defineModel<boolean>('open', { default: false })
const emit = defineEmits<{ closeAutoFocus: [event: Event] }>()
const summary = ref('')
const description = ref('')
const page = ref('')
// The outcome is kept as a state, not as text, so the notice follows a locale
// switch while the dialog stays open.
const outcome = ref<'copied' | 'unavailable' | ''>('')
const fallback = ref('')
const feedback = computed(() =>
  outcome.value === 'copied'
    ? t('view.reportBug.copied')
    : outcome.value === 'unavailable'
      ? t('view.reportBug.copyUnavailable')
      : '',
)

// Each opening reports the page the visitor is on and starts without a notice.
watch(open, (value) => {
  if (!value) return
  page.value = window.location.pathname
  outcome.value = ''
  fallback.value = ''
})

async function copyReport() {
  const report = t('view.reportBug.report', {
    summary: summary.value.trim(),
    page: page.value,
    description: description.value.trim(),
  })
  try {
    await navigator.clipboard.writeText(report)
    fallback.value = ''
    outcome.value = 'copied'
  } catch {
    fallback.value = report
    outcome.value = 'unavailable'
  }
}
</script>

<template>
  <UiDialog
    v-model:open="open"
    :title="t('view.reportBug.title')"
    @close-auto-focus="emit('closeAutoFocus', $event)"
  >
    <p class="text-sm text-muted-foreground mb-4">
      {{ t('view.reportBug.intro') }}
    </p>

    <form class="flex flex-col gap-4" @submit.prevent="copyReport">
      <UiField id="bug-summary" :label="t('view.reportBug.summary')" required>
        <UiInput id="bug-summary" v-model="summary" required maxlength="200" />
      </UiField>

      <UiField
        id="bug-description"
        :label="t('view.reportBug.description')"
        required
      >
        <UiTextarea
          id="bug-description"
          v-model="description"
          required
          maxlength="5000"
          :rows="5"
          :placeholder="t('view.reportBug.descriptionPlaceholder')"
        />
      </UiField>

      <p class="text-xs text-muted-foreground">
        {{ t('view.reportBug.pageLine', { page }) }}
      </p>

      <UiButton type="submit" variant="primary">{{
        t('view.reportBug.copy')
      }}</UiButton>

      <p v-if="feedback" role="status" class="text-xs text-foreground">
        {{ feedback }}
      </p>

      <template v-if="fallback">
        <UiField id="bug-report-copy" :label="t('view.reportBug.reportText')">
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
