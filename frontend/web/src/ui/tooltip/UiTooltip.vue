<script setup lang="ts">
import { computed } from 'vue'
import {
  TooltipContent,
  TooltipPortal,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import UiKbd from '../kbd/UiKbd.vue'
import type { Shortcut } from '../../navigation/shortcuts'
import { keysOf } from '../../navigation/shortcuts'
import { useI18n } from 'vue-i18n'

export interface UiTooltipProps {
  label: string
  hint?: string
  shortcut?: Shortcut | string[]
  keyLabel?: (key: string) => string
  side?: 'top' | 'right' | 'bottom' | 'left'
  inline?: boolean
  delayDuration?: number
  defaultOpen?: boolean
  open?: boolean
}

const props = withDefaults(defineProps<UiTooltipProps>(), {
  hint: undefined,
  shortcut: undefined,
  keyLabel: undefined,
  side: 'bottom',
  inline: false,
  delayDuration: undefined,
  defaultOpen: undefined,
  open: undefined,
})

const { t } = useI18n({ useScope: 'global' })

const defaultKeyMap: Record<string, string> = {
  Ctrl: 'ui.tooltip.control',
  Alt: 'ui.tooltip.alt',
  Shift: 'ui.tooltip.shift',
  Esc: 'ui.tooltip.escape',
  '⌥': 'ui.tooltip.optionMark',
  '⇧': 'ui.tooltip.shiftMark',
  '⌘': 'ui.tooltip.commandMark',
  '\\': 'ui.tooltip.backslashMark',
  '←': 'ui.tooltip.leftMark',
  '→': 'ui.tooltip.rightMark',
  '↑': 'ui.tooltip.upMark',
  '↓': 'ui.tooltip.downMark',
  '↵': 'ui.tooltip.enterMark',
}

function resolveKey(key: string): string {
  if (props.keyLabel) {
    return props.keyLabel(key)
  }
  if (Object.hasOwn(defaultKeyMap, key)) {
    return t(defaultKeyMap[key])
  }
  return key
}

const shortcutKeys = computed<string[]>(() => {
  if (!props.shortcut) return []
  if (Array.isArray(props.shortcut)) {
    return props.shortcut
  }
  return keysOf(props.shortcut).map(resolveKey)
})

// Reka derives the hidden tooltip text from the rendered element's
// textContent once, so it keeps the previous locale's keys. An explicit
// label tracks the translated keys.
const tooltipAriaLabel = computed(() =>
  [props.label, props.hint, shortcutKeys.value.join(' ')]
    .filter(Boolean)
    .join(' '),
)
</script>

<template>
  <TooltipRoot
    :delay-duration="delayDuration"
    :ignore-non-keyboard-focus="false"
    :default-open="defaultOpen"
    :open="open"
  >
    <TooltipTrigger as-child>
      <slot />
    </TooltipTrigger>
    <TooltipPortal :disabled="inline">
      <TooltipContent
        :aria-label="tooltipAriaLabel"
        :side="side"
        :side-offset="6"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border rounded-control shadow-md px-2.5 py-1.5 text-xs z-(--z-overlay) flex flex-wrap items-center gap-2 select-none max-w-72"
      >
        <span class="font-medium"
          ><slot name="label">{{ label }}</slot></span
        >
        <span v-if="hint || $slots.hint" class="text-muted-foreground"
          ><slot name="hint">{{ hint }}</slot></span
        >
        <span v-if="shortcutKeys.length" class="inline-flex items-center gap-1">
          <UiKbd v-for="key in shortcutKeys" :key="key">{{ key }}</UiKbd>
        </span>
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>
