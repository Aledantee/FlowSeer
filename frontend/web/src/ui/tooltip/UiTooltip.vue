<script setup lang="ts">
import { computed } from 'vue'
import {
  TooltipContent,
  TooltipPortal,
  TooltipProvider,
  TooltipRoot,
  TooltipTrigger,
} from 'reka-ui'
import UiKbd from '../kbd/UiKbd.vue'
import type { Shortcut } from '../../navigation/shortcuts'
import { keysOf } from '../../navigation/shortcuts'

export interface UiTooltipProps {
  label: string
  hint?: string
  shortcut?: Shortcut | string[]
  side?: 'top' | 'right' | 'bottom' | 'left'
  inline?: boolean
  delayDuration?: number
}

const props = withDefaults(defineProps<UiTooltipProps>(), {
  hint: undefined,
  shortcut: undefined,
  side: 'bottom',
  inline: false,
  delayDuration: 200,
})

const shortcutKeys = computed<string[]>(() => {
  if (!props.shortcut) return []
  if (Array.isArray(props.shortcut)) return props.shortcut
  return keysOf(props.shortcut)
})
</script>

<template>
  <TooltipProvider :delay-duration="delayDuration">
    <TooltipRoot
      :delay-duration="delayDuration"
      :ignore-non-keyboard-focus="false"
    >
      <TooltipTrigger as-child>
        <slot />
      </TooltipTrigger>
      <TooltipPortal :disabled="inline">
        <TooltipContent
          :side="side"
          :side-offset="6"
          :collision-padding="8"
          class="bg-popover text-foreground border border-border rounded-control shadow-md px-2.5 py-1.5 text-xs z-50 flex items-center gap-2 select-none"
        >
          <span class="font-medium">{{ label }}</span>
          <span v-if="hint" class="text-muted-foreground">{{ hint }}</span>
          <span
            v-if="shortcutKeys.length"
            class="inline-flex items-center gap-1"
          >
            <UiKbd v-for="key in shortcutKeys" :key="key">{{ key }}</UiKbd>
          </span>
        </TooltipContent>
      </TooltipPortal>
    </TooltipRoot>
  </TooltipProvider>
</template>
