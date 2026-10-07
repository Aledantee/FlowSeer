<script lang="ts">
import type { AiResult } from '../../ai'

export function serializeAiResultToText(result: AiResult): string {
  if (result.type === 'answer') {
    return result.text
  }
  const parts: string[] = []
  if (result.headline) {
    parts.push(result.headline)
  }
  const findings = result.findings ?? []
  if (findings.length > 0) {
    const findingsLines = findings.map((f: unknown) => {
      const item = f as Record<string, unknown>
      const prefix = item.severity ? `[${item.severity}] ` : ''
      const title = item.title ?? item.text ?? ''
      const detail = item.detail ? `: ${item.detail}` : ''
      return `- ${prefix}${title}${detail}`
    })
    parts.push(`Findings:\n${findingsLines.join('\n')}`)
  }
  if (result.cause) {
    const conf = result.cause.confidence
      ? ` (${result.cause.confidence} confidence)`
      : ''
    parts.push(`Likely cause${conf}: ${result.cause.text}`)
  }
  if (result.impact) {
    parts.push(`Impact: ${result.impact.text}`)
  }
  const metrics = (result as unknown as Record<string, unknown>).metrics
  if (metrics) {
    const metricLines: string[] = []
    if (Array.isArray(metrics)) {
      for (const m of metrics) {
        metricLines.push(`- ${m.label}: ${m.value}`)
      }
    } else {
      for (const [k, v] of Object.entries(metrics)) {
        metricLines.push(`- ${k}: ${v}`)
      }
    }
    if (metricLines.length > 0) {
      parts.push(`Metrics:\n${metricLines.join('\n')}`)
    }
  }
  const next =
    (result as unknown as Record<string, unknown>).next ??
    (result as unknown as Record<string, unknown>).nextSteps
  if (next && Array.isArray(next) && next.length > 0) {
    const stepLines = next.map((s: unknown, idx: number) => {
      const label =
        typeof s === 'string'
          ? s
          : ((s as Record<string, unknown>)?.label ?? '')
      return `${idx + 1}. ${label}`
    })
    parts.push(`Next steps:\n${stepLines.join('\n')}`)
  }
  return parts.join('\n\n')
}
</script>

<script setup lang="ts">
import { computed, ref, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AiRun } from '../../ai'
import { useAiRegistry } from './context'
import type { UiAiEmits, UiAiProps } from './context'
import { useAiOrigin } from './useAiOrigin'
import { useAiTarget } from './useAiTarget'

export interface UiAiResultActionsProps extends UiAiProps {
  run?: AiRun
  result?: AiResult | null
  requestId?: string
}

const props = withDefaults(defineProps<UiAiResultActionsProps>(), {
  run: undefined,
  result: undefined,
  requestId: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'copy', text: string): void
    (e: 'regenerate'): void
    (e: 'feedback', payload: { requestId: string; rating: 'up' | 'down' }): void
  }
>()

const root = useTemplateRef('root')
useAiTarget(root, () => props.ai)
useAiOrigin(
  root,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const { t } = useI18n({ useScope: 'global' })
const registry = useAiRegistry()

const copied = ref(false)
const activeRating = ref<'up' | 'down' | null>(null)
const reqId = computed(() => props.requestId ?? props.run?.requestId)

async function handleCopy() {
  if (!props.result) return
  const text = serializeAiResultToText(props.result)
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
    }
  } catch {
    // Clipboard permission or availability issue
  }
  copied.value = true
  emit('copy', text)
  setTimeout(() => {
    copied.value = false
  }, 2000)
}

function handleRegenerate() {
  emit('regenerate')
}

function handleFeedback(rating: 'up' | 'down') {
  const id = reqId.value
  if (!id) return
  activeRating.value = rating
  registry.feedback(id, rating)
  emit('feedback', { requestId: id, rating })
}
</script>

<template>
  <div
    ref="root"
    class="flex items-center gap-1 text-muted-foreground pt-1"
    data-ai-result-actions
  >
    <button
      type="button"
      class="inline-flex items-center gap-1 px-2 py-1 text-2xs font-medium rounded-control border border-border bg-card hover:bg-hover text-foreground cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      :aria-label="
        copied ? t('ui.aiResultActions.copied') : t('ui.aiResultActions.copy')
      "
      data-action="copy"
      @click="handleCopy"
    >
      <svg
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <rect width="14" height="14" x="8" y="8" rx="2" ry="2" />
        <path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2" />
      </svg>
      <span>{{
        copied ? t('ui.aiResultActions.copied') : t('ui.aiResultActions.copy')
      }}</span>
    </button>

    <button
      type="button"
      class="inline-flex items-center gap-1 px-2 py-1 text-2xs font-medium rounded-control border border-border bg-card hover:bg-hover text-foreground cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      :aria-label="t('ui.aiResultActions.regenerate')"
      data-action="regenerate"
      @click="handleRegenerate"
    >
      <svg
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8" />
        <path d="M21 3v5h-5" />
        <path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16" />
        <path d="M3 21v-5h5" />
      </svg>
      <span>{{ t('ui.aiResultActions.regenerate') }}</span>
    </button>

    <div class="h-3 w-px bg-border mx-0.5" aria-hidden="true"></div>

    <button
      type="button"
      class="inline-flex items-center justify-center h-6 w-6 rounded-control border cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      :class="
        activeRating === 'up'
          ? 'border-primary bg-primary/10 text-primary'
          : 'border-border bg-card hover:bg-hover text-muted-foreground hover:text-foreground'
      "
      :aria-label="t('ui.aiResultActions.thumbsUp')"
      :aria-pressed="activeRating === 'up'"
      data-action="thumbs-up"
      @click="handleFeedback('up')"
    >
      <svg
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M7 10v12" />
        <path
          d="M15 5.88 14 10h5.83a2 2 0 0 1 1.92 2.56l-2.33 8A2 2 0 0 1 17.5 22H4a2 2 0 0 1-2-2v-8a2 2 0 0 1 2-2h3"
        />
      </svg>
    </button>

    <button
      type="button"
      class="inline-flex items-center justify-center h-6 w-6 rounded-control border cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      :class="
        activeRating === 'down'
          ? 'border-danger-border bg-danger-surface text-danger-foreground'
          : 'border-border bg-card hover:bg-hover text-muted-foreground hover:text-foreground'
      "
      :aria-label="t('ui.aiResultActions.thumbsDown')"
      :aria-pressed="activeRating === 'down'"
      data-action="thumbs-down"
      @click="handleFeedback('down')"
    >
      <svg
        class="h-3 w-3"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        aria-hidden="true"
      >
        <path d="M17 14V2" />
        <path
          d="M9 18.12 10 14H4.17a2 2 0 0 1-1.92-2.56l2.33-8A2 2 0 0 1 6.5 2H20a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2h-3"
        />
      </svg>
    </button>
  </div>
</template>
