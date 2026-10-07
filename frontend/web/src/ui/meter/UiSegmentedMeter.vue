<script setup lang="ts">
import { computed, useTemplateRef } from 'vue'
import { useI18n } from 'vue-i18n'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export type SegmentTone = 'success' | 'warning' | 'danger' | 'info' | 'empty'

export interface MeterSegment {
  label: string
  count: number
  tone: SegmentTone
}

export interface UiSegmentedMeterProps extends UiAiProps {
  segments?: MeterSegment[]
  counts?: Record<string, number>
  labels?: Record<string, string>
  segmentText?: (count: number, label: string) => string
  legend?: boolean
  label?: string
}

const props = withDefaults(defineProps<UiSegmentedMeterProps>(), {
  segments: undefined,
  counts: undefined,
  labels: undefined,
  segmentText: undefined,
  legend: false,
  label: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<UiAiEmits>()

const anchor = useTemplateRef('anchor')
useAiTarget(anchor, () => props.ai)
useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const { t, n, locale } = useI18n({ useScope: 'global' })

const defaultStatusKeys = {
  Healthy: 'ui.segmentedMeter.healthy',
  Degraded: 'ui.segmentedMeter.degraded',
  Offline: 'ui.segmentedMeter.offline',
} as const

// Segment labels can be data-derived, so a key such as "constructor" must not
// resolve to a member of Object.prototype.
function overrideLabel(key: string): string | undefined {
  const labels = props.labels
  return labels && Object.hasOwn(labels, key) ? labels[key] : undefined
}

function resolveLabel(key: string): string {
  const override = overrideLabel(key)
  if (override !== undefined) {
    return override
  }
  if (Object.hasOwn(defaultStatusKeys, key)) {
    return t(defaultStatusKeys[key as keyof typeof defaultStatusKeys])
  }
  return key
}

function formatSegmentText(count: number, label: string): string {
  if (props.segmentText) {
    return props.segmentText(count, label)
  }
  return t('ui.segmentedMeter.segmentText', {
    count: n(count, 'decimal'),
    label,
  })
}

const normalizedSegments = computed<MeterSegment[]>(() => {
  if (props.segments) {
    return props.segments.map((seg) => ({
      ...seg,
      label: overrideLabel(seg.label) ?? seg.label,
    }))
  }
  if (props.counts) {
    const defaultOrder: { key: string; tone: SegmentTone }[] = [
      { key: 'Healthy', tone: 'success' },
      { key: 'Degraded', tone: 'warning' },
      { key: 'Offline', tone: 'danger' },
    ]
    return defaultOrder.map(({ key, tone }) => ({
      label: resolveLabel(key),
      count: props.counts?.[key] ?? 0,
      tone,
    }))
  }
  return []
})

const total = computed(() =>
  normalizedSegments.value.reduce((sum, s) => sum + s.count, 0),
)

const summary = computed(() => {
  // Truthiness on purpose: the summary is the role="img" track's only name, and
  // an empty one fails axe's role-img-alt, so an empty label falls back.
  if (props.label) return props.label
  const nonZero = normalizedSegments.value.filter((s) => s.count > 0)
  if (nonZero.length === 0) return n(0, 'decimal')
  const phrases = nonZero.map((s) => formatSegmentText(s.count, s.label))
  const formatter = new Intl.ListFormat(locale.value, {
    type: 'unit',
    style: 'short',
  })
  return formatter.format(phrases)
})

function getToneClass(tone: SegmentTone): string {
  switch (tone) {
    case 'success':
      return 'bg-success-foreground'
    case 'warning':
      return 'bg-warning-foreground'
    case 'danger':
      return 'bg-danger-foreground'
    case 'info':
      return 'bg-accent'
    case 'empty':
    default:
      return 'bg-subtle'
  }
}
</script>

<template>
  <div ref="anchor" class="w-full">
    <div
      role="img"
      :aria-label="summary"
      class="flex h-1.5 w-full overflow-hidden rounded-full bg-subtle gap-0.5"
    >
      <template v-if="total > 0">
        <template v-for="seg in normalizedSegments" :key="seg.label">
          <span
            v-if="seg.count > 0"
            :class="[
              'health-segment h-full transition-all',
              getToneClass(seg.tone),
            ]"
            :style="{ flexGrow: seg.count }"
            :title="formatSegmentText(seg.count, seg.label)"
          />
        </template>
      </template>
      <span v-else class="health-segment empty h-full w-full bg-subtle" />
    </div>

    <ul
      v-if="legend"
      class="flex flex-wrap gap-4 text-xs text-muted-foreground mt-2"
    >
      <li
        v-for="seg in normalizedSegments"
        :key="seg.label"
        class="inline-flex items-center gap-1.5"
      >
        <i
          class="w-2 h-2 rounded-full inline-block shrink-0"
          :class="getToneClass(seg.tone)"
          aria-hidden="true"
        />
        <span>{{ seg.label }}</span>
        <strong class="font-semibold text-foreground ml-0.5">{{
          n(seg.count, 'decimal')
        }}</strong>
      </li>
    </ul>
  </div>
</template>
