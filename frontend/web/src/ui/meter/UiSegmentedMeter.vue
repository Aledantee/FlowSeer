<script setup lang="ts">
import { computed } from 'vue'

export type SegmentTone = 'success' | 'warning' | 'danger' | 'info' | 'empty'

export interface MeterSegment {
  label: string
  count: number
  tone: SegmentTone
}

export interface UiSegmentedMeterProps {
  segments?: MeterSegment[]
  counts?: Record<string, number>
  legend?: boolean
  label?: string
}

const props = withDefaults(defineProps<UiSegmentedMeterProps>(), {
  segments: undefined,
  counts: undefined,
  legend: false,
  label: undefined,
})

const normalizedSegments = computed<MeterSegment[]>(() => {
  if (props.segments) {
    return props.segments
  }
  if (props.counts) {
    const defaultOrder: { key: string; tone: SegmentTone }[] = [
      { key: 'Healthy', tone: 'success' },
      { key: 'Degraded', tone: 'warning' },
      { key: 'Offline', tone: 'danger' },
    ]
    return defaultOrder.map(({ key, tone }) => ({
      label: key,
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
  if (props.label) return props.label
  const nonZero = normalizedSegments.value.filter((s) => s.count > 0)
  if (nonZero.length === 0) return '0'
  return nonZero.map((s) => `${s.count} ${s.label}`).join(', ')
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
  <div class="w-full">
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
            :title="`${seg.count} ${seg.label}`"
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
          seg.count
        }}</strong>
      </li>
    </ul>
  </div>
</template>
