<script setup lang="ts">
import { I18nT, useI18n } from 'vue-i18n'
import UiCard from './UiCard.vue'

export interface UiMetricCardProps {
  label: string
  value: number
  unit?: string
  valueText?: (value: number, unit?: string) => string
}

defineProps<UiMetricCardProps>()

const { n } = useI18n({ useScope: 'global' })
</script>

<template>
  <UiCard as="article" class="metric-card">
    <div
      class="flex items-center justify-between text-xs text-muted-foreground font-medium"
    >
      <span>{{ label }}</span>
      <span v-if="$slots.icon" class="metric-icon">
        <slot name="icon" />
      </span>
    </div>
    <div class="flex items-baseline gap-1 mt-1">
      <span
        v-if="valueText"
        class="font-mono text-2xl font-semibold tabular-nums text-foreground metric-value"
      >
        {{ valueText(value, unit) }}
      </span>
      <I18nT
        v-else-if="unit"
        scope="global"
        keypath="ui.metricCard.valueWithUnit"
      >
        <template #value>
          <span
            class="font-mono text-2xl font-semibold tabular-nums text-foreground metric-value"
          >
            {{ n(value, 'decimal') }}
          </span>
        </template>
        <template #unit>
          <span class="text-xs text-muted-foreground font-mono">
            {{ unit }}
          </span>
        </template>
      </I18nT>
      <span
        v-else
        class="font-mono text-2xl font-semibold tabular-nums text-foreground metric-value"
      >
        {{ n(value, 'decimal') }}
      </span>
    </div>
    <div v-if="$slots.default" class="text-xs text-muted-foreground mt-1">
      <slot />
    </div>
  </UiCard>
</template>
