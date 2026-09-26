<script setup lang="ts">
import {
  TooltipContent,
  TooltipPortal,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import type { Shortcut } from '../navigation/shortcuts'
import { keysOf } from '../navigation/shortcuts'

// The one hover label for controls: what it does, an optional hint, and the
// keyboard shortcut that does the same. It wraps its single child as the
// trigger, so the child keeps its own element and accessible name.
withDefaults(
  defineProps<{
    label: string
    hint?: string
    shortcut?: Shortcut
    side?: 'top' | 'right' | 'bottom' | 'left'
    // Render beside the trigger instead of at the end of the page, for
    // triggers inside a modal dialog or popover, whose top layer would
    // otherwise cover the tooltip.
    inline?: boolean
  }>(),
  { hint: undefined, shortcut: undefined, side: 'bottom', inline: false },
)
</script>

<template>
  <TooltipRoot>
    <TooltipTrigger as-child><slot /></TooltipTrigger>
    <TooltipPortal :disabled="inline">
      <TooltipContent
        class="tooltip"
        :side="side"
        :side-offset="6"
        :collision-padding="8"
      >
        <span class="tooltip-label">{{ label }}</span>
        <span v-if="hint" class="tooltip-hint">{{ hint }}</span>
        <span v-if="shortcut" class="tooltip-keys"
          ><kbd v-for="key in keysOf(shortcut)" :key="key">{{ key }}</kbd></span
        >
      </TooltipContent>
    </TooltipPortal>
  </TooltipRoot>
</template>
