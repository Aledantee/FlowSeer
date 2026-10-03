<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AiStaleError, AiUnavailableError } from '../../ai'
import type { AiResult, AiRun, AiTarget } from '../../ai'
import UiButton from '../button/UiButton.vue'
import { useAiRegistry } from './context'
import UiAiLabel from './UiAiLabel.vue'
import UiAiResult, { type UiAiResultState } from './UiAiResult.vue'
import UiAiResultActions from './UiAiResultActions.vue'

export interface UiAiSummaryLabels {
  summaryLabel?: string
  generate?: string
  generating?: string
  unavailable?: string
  retry?: string
  error?: string
  stop?: string
}

export interface UiAiSummaryProps {
  target: AiTarget
  labels?: UiAiSummaryLabels
}

const props = defineProps<UiAiSummaryProps>()

const { t } = useI18n({ useScope: 'global' })
const registry = useAiRegistry()

const summaryLabelText = computed(
  () =>
    props.labels?.summaryLabel ??
    t('ui.aiSummary.summaryLabel', { label: props.target.label }),
)

type SummaryState =
  | { kind: 'idle' }
  | { kind: 'running'; run: AiRun }
  | { kind: 'stopped'; run: AiRun }
  | { kind: 'done'; run: AiRun }
  | { kind: 'unavailable'; run?: AiRun }
  | { kind: 'error'; run?: AiRun; message: string }

const state = ref<SummaryState>({ kind: 'idle' })
const currentRun = ref<AiRun | null>(null)
const latestResult = ref<AiResult | null>(null)

let token = 0

// A finished result is kept while the target's context updates;
// it resets only when the target id changes.
watch(
  () => props.target.id,
  () => {
    token += 1
    if (currentRun.value) {
      currentRun.value.stop()
    }
    currentRun.value = null
    latestResult.value = null
    state.value = { kind: 'idle' }
  },
)

const resultState = computed<UiAiResultState>(() => {
  switch (state.value.kind) {
    case 'running':
      return 'generating'
    case 'done':
      return 'done'
    case 'stopped':
      return 'stopped'
    case 'unavailable':
      return 'unavailable'
    case 'error':
      return 'error'
    case 'idle':
    default:
      return 'idle'
  }
})

async function generate() {
  const requestToken = (token += 1)
  try {
    const run = registry.request(props.target, {
      action: 'summary',
      bound: true,
    })
    currentRun.value = run
    latestResult.value = null
    state.value = { kind: 'running', run }

    for await (const s of run.snapshots) {
      if (requestToken !== token) return
      latestResult.value = s
    }

    if (requestToken !== token) return
    if (state.value.kind === 'running') {
      state.value = { kind: 'done', run }
    }
  } catch (error: unknown) {
    if (requestToken !== token) return
    if (error instanceof AiStaleError) {
      state.value = { kind: 'idle' }
      latestResult.value = null
      currentRun.value = null
      return
    }
    if (error instanceof AiUnavailableError) {
      state.value = { kind: 'unavailable', run: currentRun.value ?? undefined }
      return
    }
    const message =
      error instanceof Error
        ? error.message
        : (props.labels?.error ?? t('ui.aiSummary.error'))
    state.value = {
      kind: 'error',
      run: currentRun.value ?? undefined,
      message,
    }
  }
}

function start() {
  void generate()
}

function handleStop() {
  if (currentRun.value) {
    currentRun.value.stop()
  }
  if (state.value.kind === 'running') {
    state.value = { kind: 'stopped', run: currentRun.value! }
  }
}
</script>

<template>
  <section
    class="flex flex-col items-start gap-2 w-full"
    :aria-label="summaryLabelText"
    data-ai-summary
  >
    <!-- Idle state: button to summarize -->
    <UiButton
      v-if="state.kind === 'idle'"
      size="sm"
      variant="secondary"
      data-ai-summary-trigger
      @click="start"
    >
      <span v-if="props.labels?.generate">{{ props.labels.generate }}</span>
      <i18n-t v-else keypath="ui.aiSummary.summarize" scope="global" tag="span">
        <template #label>
          <span translate="no">{{ props.target.label }}</span>
        </template>
      </i18n-t>
    </UiButton>

    <!-- Active / Done / Stopped / Error / Unavailable state -->
    <div v-else class="flex flex-col gap-2 w-full">
      <div v-if="currentRun" class="flex items-center justify-between gap-2">
        <UiAiLabel :run="currentRun" />
      </div>

      <UiAiResult
        :result="latestResult"
        :state="resultState"
        :error="state.kind === 'error' ? state.message : null"
        :show-stop="true"
        @stop="handleStop"
      />

      <UiAiResultActions
        v-if="
          (state.kind === 'done' || state.kind === 'stopped') && latestResult
        "
        :run="currentRun ?? undefined"
        :result="latestResult"
        @regenerate="start"
      />

      <div
        v-if="state.kind === 'unavailable' || state.kind === 'error'"
        class="flex items-center gap-2 pt-1"
      >
        <UiButton size="sm" variant="secondary" @click="start">
          {{ props.labels?.retry ?? t('ui.aiSummary.retry') }}
        </UiButton>
      </div>
    </div>
  </section>
</template>
