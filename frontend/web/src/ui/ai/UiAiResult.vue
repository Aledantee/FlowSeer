<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { AiUnavailableError } from '../../ai'
import type { AiAnswer, AiResult, AiRun, AiSummary, AiTone } from '../../ai'
import UiBadge, { type UiBadgeProps } from '../badge/UiBadge.vue'
import UiButton from '../button/UiButton.vue'
import UiAiEntityChip from './UiAiEntityChip.vue'

export type UiAiResultState =
  'idle' | 'generating' | 'done' | 'stopped' | 'error' | 'unavailable'

export interface UiAiResultProps {
  run?: AiRun
  result?: AiResult | null
  state?: UiAiResultState
  error?: Error | string | null
  showStop?: boolean
}

const props = withDefaults(defineProps<UiAiResultProps>(), {
  run: undefined,
  result: undefined,
  state: undefined,
  error: undefined,
  showStop: false,
})

const emit = defineEmits<{
  (e: 'stop'): void
  (e: 'update:state', state: UiAiResultState): void
}>()

const { t } = useI18n({ useScope: 'global' })

const internalResult = ref<AiResult | null>(null)
const internalState = ref<UiAiResultState>('idle')
const internalError = ref<string | null>(null)

watch(
  () => props.run,
  async (run) => {
    if (!run) return
    internalResult.value = null
    internalState.value = 'generating'
    internalError.value = null
    try {
      for await (const snapshot of run.snapshots) {
        internalResult.value = snapshot
      }
      if (internalState.value === 'generating') {
        internalState.value = 'done'
      }
    } catch (err: unknown) {
      if (err instanceof AiUnavailableError) {
        internalState.value = 'unavailable'
      } else {
        internalState.value = 'error'
        internalError.value =
          err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : t('ui.aiResult.error')
      }
    }
  },
  { immediate: true },
)

const resolvedState = computed<UiAiResultState>(() => {
  if (props.state !== undefined) return props.state
  return internalState.value
})

const resolvedResult = computed<AiResult | null>(() => {
  if (props.result !== undefined) return props.result
  return internalResult.value
})

const resolvedError = computed<string | null>(() => {
  if (props.error !== undefined) {
    if (props.error instanceof Error) return props.error.message
    return props.error
  }
  return internalError.value
})

const isAnswer = computed(() => resolvedResult.value?.type === 'answer')
const answerResult = computed(() =>
  isAnswer.value ? (resolvedResult.value as AiAnswer) : null,
)
const summaryResult = computed(() =>
  !isAnswer.value ? (resolvedResult.value as AiSummary) : null,
)

// One polite announcement per state change, announcing the state, not the content.
const announcement = ref('')
let lastAnnouncedState: UiAiResultState | null = null

watch(
  resolvedState,
  (newState) => {
    if (newState === lastAnnouncedState) return
    lastAnnouncedState = newState
    if (newState === 'generating') {
      announcement.value = t('ui.aiResult.statusGenerating')
    } else if (newState === 'done') {
      announcement.value = t('ui.aiResult.statusDone')
    } else if (newState === 'stopped') {
      announcement.value = t('ui.aiResult.statusStopped')
    } else if (newState === 'error') {
      announcement.value = t('ui.aiResult.statusError')
    } else if (newState === 'unavailable') {
      announcement.value = t('ui.aiResult.statusUnavailable')
    } else {
      announcement.value = ''
    }
  },
  { immediate: true },
)

function handleStop() {
  if (props.run) {
    props.run.stop()
  }
  internalState.value = 'stopped'
  emit('stop')
  emit('update:state', 'stopped')
}

function toneVariant(tone?: AiTone): UiBadgeProps['variant'] {
  switch (tone) {
    case 'ok':
      return 'success'
    case 'warning':
      return 'warning'
    case 'critical':
      return 'danger'
    case 'unknown':
    default:
      return 'default'
  }
}

function toneLabel(tone?: AiTone): string {
  switch (tone) {
    case 'ok':
      return t('ui.aiResult.tone.ok')
    case 'warning':
      return t('ui.aiResult.tone.warning')
    case 'critical':
      return t('ui.aiResult.tone.critical')
    case 'unknown':
    default:
      return t('ui.aiResult.tone.unknown')
  }
}

function severityVariant(sev?: string): UiBadgeProps['variant'] {
  switch (sev) {
    case 'critical':
      return 'danger'
    case 'warning':
      return 'warning'
    case 'info':
    default:
      return 'info'
  }
}

function findingTitle(f: unknown): string {
  if (!f || typeof f !== 'object') return ''
  const rec = f as Record<string, unknown>
  const val = rec.title ?? rec.text
  return typeof val === 'string' ? val : ''
}

const findingsList = computed(() => summaryResult.value?.findings ?? [])

const causeHeaderText = computed(() => {
  if (!summaryResult.value?.cause) return ''
  const base = t('ui.aiResult.cause')
  const conf = summaryResult.value.cause.confidence
  if (!conf) return base
  let confText: string = conf
  if (conf === 'low') confText = t('ui.aiResult.confidenceLow')
  else if (conf === 'medium') confText = t('ui.aiResult.confidenceMedium')
  else if (conf === 'high') confText = t('ui.aiResult.confidenceHigh')
  const confidenceStr = t('ui.aiResult.confidence', { confidence: confText })
  return `${base} · ${confidenceStr}`
})

const metricsList = computed(() => {
  const m = summaryResult.value?.metrics
  if (!m) return []
  if (Array.isArray(m)) {
    return m.map((item) => ({ label: item.label, value: item.value }))
  }
  return Object.entries(m as Record<string, unknown>).map(([label, value]) => ({
    label,
    value: String(value),
  }))
})

const nextStepsList = computed(() => {
  const n =
    (summaryResult.value as unknown as Record<string, unknown>)?.next ??
    (summaryResult.value as unknown as Record<string, unknown>)?.nextSteps
  if (!n || !Array.isArray(n)) return []
  return n.map((item: unknown) =>
    typeof item === 'string'
      ? { label: item, ref: undefined }
      : (item as { label: string; ref?: unknown }),
  )
})

const sourcesList = computed(() => {
  const s =
    (summaryResult.value as unknown as Record<string, unknown>)?.sources ??
    (summaryResult.value as unknown as Record<string, unknown>)?.refs
  if (!s || !Array.isArray(s)) return []
  return s
})
</script>

<template>
  <div
    class="flex flex-col gap-3"
    :aria-busy="resolvedState === 'generating'"
    data-ai-result
  >
    <div role="status" aria-live="polite" class="sr-only" data-ai-announcement>
      {{ announcement }}
    </div>

    <!-- Skeleton while pending with no snapshot yet -->
    <div
      v-if="resolvedState === 'generating' && !resolvedResult"
      class="flex flex-col gap-2.5"
      data-ai-result-skeleton
    >
      <div class="flex items-center gap-2">
        <span
          class="ai-shimmer h-5 w-16 rounded-full"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-4 w-48 rounded-control"
          aria-hidden="true"
        ></span>
      </div>
      <div class="flex flex-col gap-1.5">
        <span
          class="ai-shimmer h-3 w-full rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-5/6 rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-2/3 rounded-control"
          aria-hidden="true"
        ></span>
      </div>
      <UiButton
        v-if="showStop"
        size="sm"
        variant="ghost"
        class="self-start text-xs"
        @click="handleStop"
      >
        {{ t('ui.aiResult.stop') }}
      </UiButton>
    </div>

    <!-- Error state -->
    <div
      v-else-if="resolvedState === 'error'"
      role="alert"
      class="text-xs text-danger-foreground font-medium"
      data-ai-result-error
    >
      {{ resolvedError || t('ui.aiResult.error') }}
    </div>

    <!-- Unavailable state -->
    <div
      v-else-if="resolvedState === 'unavailable'"
      role="status"
      class="text-xs text-muted-foreground"
      data-ai-result-unavailable
    >
      {{ t('ui.aiResult.unavailable') }}
    </div>

    <!-- Result display -->
    <template v-else-if="resolvedResult">
      <!-- Stopped banner/badge -->
      <div
        v-if="resolvedState === 'stopped'"
        class="flex items-center gap-2"
        data-ai-result-stopped
      >
        <UiBadge variant="outline" size="sm">
          {{ t('ui.aiResult.stopped') }}
        </UiBadge>
      </div>

      <!-- Freeform Answer -->
      <template v-if="isAnswer && answerResult">
        <p
          v-if="answerResult.text"
          class="text-xs text-foreground whitespace-pre-wrap ai-fade-in motion-reduce:animate-none"
          data-ai-result-text
        >
          {{ answerResult.text }}
        </p>
        <div
          v-if="answerResult.refs && answerResult.refs.length > 0"
          class="flex flex-wrap gap-1.5 ai-fade-in motion-reduce:animate-none"
          data-ai-result-refs
        >
          <UiAiEntityChip
            v-for="r in answerResult.refs"
            :key="r.id"
            :entity="r"
          />
        </div>
      </template>

      <!-- Structured Summary -->
      <template v-else-if="summaryResult">
        <!-- Tone & Headline -->
        <div
          v-if="summaryResult.tone || summaryResult.headline"
          class="flex flex-col gap-1 ai-fade-in motion-reduce:animate-none"
          data-ai-result-header
        >
          <div v-if="summaryResult.tone" class="flex items-center gap-2">
            <UiBadge
              :variant="toneVariant(summaryResult.tone)"
              size="sm"
              data-ai-result-tone
            >
              {{ toneLabel(summaryResult.tone) }}
            </UiBadge>
          </div>
          <h4
            v-if="summaryResult.headline"
            class="text-sm font-semibold text-foreground leading-snug"
            data-ai-result-headline
          >
            {{ summaryResult.headline }}
          </h4>
        </div>

        <!-- Findings List -->
        <div
          v-if="findingsList.length > 0"
          class="flex flex-col gap-1.5 ai-fade-in motion-reduce:animate-none"
          data-ai-result-findings
        >
          <ul class="list-disc pl-4 space-y-1 text-xs text-foreground">
            <li v-for="(finding, idx) in findingsList" :key="idx">
              <div class="inline-flex items-center gap-1.5 flex-wrap">
                <UiBadge
                  v-if="finding.severity"
                  :variant="severityVariant(finding.severity)"
                  size="sm"
                  data-ai-finding-severity
                >
                  {{ finding.severity }}
                </UiBadge>
                <span>{{ findingTitle(finding) }}</span>
                <span v-if="finding.detail" class="text-muted-foreground">{{
                  finding.detail
                }}</span>
                <UiAiEntityChip
                  v-for="r in finding.refs"
                  :key="r.id"
                  :entity="r"
                  size="sm"
                />
              </div>
            </li>
          </ul>
        </div>

        <!-- Cause Section -->
        <div
          v-if="summaryResult.cause && summaryResult.cause.text"
          class="flex flex-col gap-0.5 text-xs ai-fade-in motion-reduce:animate-none"
          data-ai-result-cause
        >
          <span
            class="font-medium text-muted-foreground"
            data-ai-cause-header
            >{{ causeHeaderText }}</span
          >
          <p class="text-foreground">
            {{ summaryResult.cause.text }}
          </p>
          <div
            v-if="
              summaryResult.cause.refs && summaryResult.cause.refs.length > 0
            "
            class="flex flex-wrap gap-1 mt-0.5"
          >
            <UiAiEntityChip
              v-for="r in summaryResult.cause.refs"
              :key="r.id"
              :entity="r"
              size="sm"
            />
          </div>
        </div>

        <!-- Impact Section -->
        <div
          v-if="summaryResult.impact && summaryResult.impact.text"
          class="flex flex-col gap-0.5 text-xs ai-fade-in motion-reduce:animate-none"
          data-ai-result-impact
        >
          <span class="font-medium text-muted-foreground">{{
            t('ui.aiResult.impact')
          }}</span>
          <p class="text-foreground">
            {{ summaryResult.impact.text }}
          </p>
          <div
            v-if="
              summaryResult.impact.refs && summaryResult.impact.refs.length > 0
            "
            class="flex flex-wrap gap-1 mt-0.5"
          >
            <UiAiEntityChip
              v-for="r in summaryResult.impact.refs"
              :key="r.id"
              :entity="r"
              size="sm"
            />
          </div>
        </div>

        <!-- Metrics Definition List -->
        <div
          v-if="metricsList.length > 0"
          class="flex flex-col gap-1 ai-fade-in motion-reduce:animate-none"
          data-ai-result-metrics
        >
          <dl class="grid grid-cols-2 gap-x-2 gap-y-1 text-xs">
            <template v-for="item in metricsList" :key="item.label">
              <dt class="text-muted-foreground">
                {{ item.label }}
              </dt>
              <dd class="font-medium text-foreground">
                {{ item.value }}
              </dd>
            </template>
          </dl>
        </div>

        <!-- Next Steps Section -->
        <div
          v-if="nextStepsList.length > 0"
          class="flex flex-col gap-1 text-xs ai-fade-in motion-reduce:animate-none"
          data-ai-result-next-steps
        >
          <span class="font-medium text-muted-foreground">{{
            t('ui.aiResult.nextSteps')
          }}</span>
          <ol class="list-decimal pl-4 space-y-0.5 text-foreground">
            <li v-for="(step, idx) in nextStepsList" :key="idx">
              <span>{{ step.label }}</span>
            </li>
          </ol>
        </div>

        <!-- Sources / Global Refs -->
        <div
          v-if="sourcesList.length > 0"
          class="flex flex-wrap gap-1.5 ai-fade-in motion-reduce:animate-none"
          data-ai-result-refs
        >
          <UiAiEntityChip v-for="r in sourcesList" :key="r.id" :entity="r" />
        </div>
      </template>

      <!-- Stop button while generating with a partial snapshot -->
      <UiButton
        v-if="showStop && resolvedState === 'generating'"
        size="sm"
        variant="ghost"
        class="self-start text-xs"
        @click="handleStop"
      >
        {{ t('ui.aiResult.stop') }}
      </UiButton>
    </template>
  </div>
</template>
