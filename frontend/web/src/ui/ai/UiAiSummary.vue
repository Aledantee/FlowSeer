<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AiStaleError, AiUnavailableError } from '../../ai'
import type { AiTarget } from '../../ai'
import UiButton from '../button/UiButton.vue'
import { useAiRegistry } from './context'

export interface UiAiSummaryLabels {
  summaryLabel?: string
  generate?: string
  generating?: string
  unavailable?: string
  retry?: string
  error?: string
}

export interface UiAiSummaryProps {
  target: AiTarget
  labels?: UiAiSummaryLabels
}

// An on-demand summary. It makes no request until the operator activates
// Generate summary, so a page with several placements stays quiet until one
// is wanted. A retry is a fresh request, with its own request id.
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
  | { kind: 'loading' }
  | { kind: 'result'; answer: string }
  | { kind: 'error'; message: string }
  | { kind: 'generic-error' }
  | { kind: 'unavailable' }
const state = ref<SummaryState>({ kind: 'idle' })

// A newer request supersedes an older one; a late answer from the older
// request is dropped.
let token = 0

function targetSnapshot(target: AiTarget): string {
  return JSON.stringify([
    target.id,
    target.kind,
    target.label,
    target.segment,
    Object.entries(target.context).sort(([a], [b]) => a.localeCompare(b)),
  ])
}

watch(
  () => targetSnapshot(props.target),
  () => {
    token += 1
    state.value = { kind: 'idle' }
  },
  { flush: 'sync' },
)

async function generate() {
  const requestToken = (token += 1)
  const snapshot = targetSnapshot(props.target)
  state.value = { kind: 'loading' }
  try {
    const answer = await registry.request(props.target, { kind: 'summary' })
    if (requestToken !== token || snapshot !== targetSnapshot(props.target))
      return
    state.value = { kind: 'result', answer }
  } catch (error: unknown) {
    if (requestToken !== token || snapshot !== targetSnapshot(props.target))
      return
    if (error instanceof AiStaleError) {
      state.value = { kind: 'idle' }
      return
    }
    if (error instanceof AiUnavailableError) {
      state.value = { kind: 'unavailable' }
      return
    }
    if (error instanceof Error) {
      state.value = {
        kind: 'error',
        message: error.message,
      }
      return
    }
    state.value = { kind: 'generic-error' }
  }
}
function start() {
  void generate()
}

// A result is revealed a word at a time, the way a streamed answer arrives.
// The visible text is decorative; the full answer sits in a status region so
// a screen reader announces it once. Reduced motion shows it at once.
const revealStep = 35
const shownWords = ref(0)
let revealTimer: ReturnType<typeof setInterval> | undefined
const words = computed(() =>
  state.value.kind === 'result' ? state.value.answer.split(/(\s+)/) : [],
)
const revealing = computed(() => shownWords.value < words.value.length)
function stopReveal() {
  if (revealTimer !== undefined) clearInterval(revealTimer)
  revealTimer = undefined
}
watch(words, (next) => {
  stopReveal()
  const reduced =
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(prefers-reduced-motion: reduce)').matches
  shownWords.value = reduced ? next.length : 0
  if (reduced || !next.length) return
  revealTimer = setInterval(() => {
    shownWords.value += 2
    if (!revealing.value) stopReveal()
  }, revealStep)
})
onUnmounted(stopReveal)
</script>

<template>
  <section
    class="flex flex-col items-start gap-2"
    :aria-label="summaryLabelText"
  >
    <UiButton
      v-if="state.kind === 'idle'"
      size="sm"
      variant="secondary"
      @click="start"
    >
      {{ props.labels?.generate ?? t('ui.aiSummary.generate') }}
    </UiButton>

    <template v-else-if="state.kind === 'loading'">
      <div class="flex w-full flex-col gap-1.5" data-ai-summary-loading>
        <span
          class="ai-shimmer h-3 w-3/4 rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-full rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-2/3 rounded-control"
          aria-hidden="true"
        ></span>
      </div>
      <span
        role="status"
        class="sr-only text-2xs text-muted-foreground motion-reduce:not-sr-only"
      >
        {{ props.labels?.generating ?? t('ui.aiSummary.generating') }}
      </span>
    </template>

    <template v-else-if="state.kind === 'result'">
      <p
        class="text-xs whitespace-pre-wrap text-foreground"
        aria-hidden="true"
        data-ai-summary-text
      >
        {{ words.slice(0, shownWords).join('')
        }}<span v-if="revealing" class="ai-caret"></span>
      </p>
      <p role="status" class="sr-only">{{ state.answer }}</p>
    </template>

    <template v-else-if="state.kind === 'unavailable'">
      <p role="status" class="text-xs text-muted-foreground">
        {{ props.labels?.unavailable ?? t('ui.aiSummary.unavailable') }}
      </p>
      <UiButton size="sm" variant="ghost" @click="start">
        {{ props.labels?.retry ?? t('ui.aiSummary.retry') }}
      </UiButton>
    </template>

    <template v-else>
      <p role="alert" class="text-xs text-danger-foreground">
        {{
          state.kind === 'error'
            ? state.message
            : (props.labels?.error ?? t('ui.aiSummary.error'))
        }}
      </p>
      <UiButton size="sm" variant="secondary" @click="start">
        {{ props.labels?.retry ?? t('ui.aiSummary.retry') }}
      </UiButton>
    </template>
  </section>
</template>
