<script setup lang="ts">
import { computed } from 'vue'
import { I18nT, useI18n } from 'vue-i18n'

export interface UiMeterThresholds {
  warning: number
  critical: number
}

export interface UiMeterProps {
  label: string
  value: number
  min?: number
  max?: number
  unit?: string
  detail?: string
  detailSeparator?: string
  valueText?: (value: number, unit?: string) => string
  tone?: 'normal' | 'warning' | 'critical' | 'auto'
  thresholds?: UiMeterThresholds
}

const props = withDefaults(defineProps<UiMeterProps>(), {
  min: 0,
  max: 100,
  unit: undefined,
  detail: undefined,
  detailSeparator: undefined,
  valueText: undefined,
  tone: 'auto',
  thresholds: () => ({ warning: 75, critical: 90 }),
})

const { t, n } = useI18n({ useScope: 'global' })

const resolvedUnit = computed(() => props.unit ?? t('ui.meter.unit'))
const resolvedDetailSeparator = computed(
  () => props.detailSeparator ?? t('ui.meter.detailSeparator'),
)

const percentage = computed(() => {
  const range = props.max - props.min
  return range > 0 ? ((props.value - props.min) / range) * 100 : 0
})

const clampedPercentage = computed(() =>
  Math.min(100, Math.max(0, percentage.value)),
)

const resolvedTone = computed(() => {
  if (props.tone && props.tone !== 'auto') {
    return props.tone
  }
  const pct = percentage.value
  if (pct >= props.thresholds.critical) return 'critical'
  if (pct >= props.thresholds.warning) return 'warning'
  return 'normal'
})

const toneColorClass = computed(() => {
  switch (resolvedTone.value) {
    case 'critical':
      return 'bg-danger-foreground'
    case 'warning':
      return 'bg-warning-foreground'
    case 'normal':
    default:
      return 'bg-chart-1'
  }
})
</script>

<template>
  <div class="w-full space-y-1">
    <div class="flex items-center justify-between text-xs">
      <span class="font-medium text-foreground">{{ label }}</span>
      <span class="font-mono tabular-nums text-muted-foreground">
        <template v-if="valueText">
          {{ valueText(value, resolvedUnit) }}
        </template>
        <I18nT v-else scope="global" keypath="ui.meter.valueWithUnit">
          <template #value>{{ n(value, 'decimal') }}</template>
          <template #unit>{{ resolvedUnit }}</template>
        </I18nT>
        <span v-if="detail" class="font-sans text-muted-foreground">
          {{ resolvedDetailSeparator }}{{ detail }}
        </span>
      </span>
    </div>
    <span
      role="meter"
      :aria-label="label"
      :aria-valuemin="min"
      :aria-valuemax="max"
      :aria-valuenow="value"
      class="block h-1.5 w-full overflow-hidden rounded-full bg-subtle"
    >
      <i
        class="block h-full rounded-full transition-all duration-300"
        :class="toneColorClass"
        :style="{ width: `${clampedPercentage}%` }"
      />
    </span>
  </div>
</template>
