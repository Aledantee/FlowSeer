<script setup lang="ts">
import {
  PopoverAnchor,
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
} from 'reka-ui'
import type { AiTargetElement } from '../../ai'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { PopupAnchor } from './popupAnchor'

export interface UiPopoverProps extends UiAiProps {
  open?: boolean
  defaultOpen?: boolean
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  sideOffset?: number
  reference?: AiTargetElement | { getBoundingClientRect: () => DOMRect } | null
}

withDefaults(defineProps<UiPopoverProps>(), {
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'center',
  sideOffset: 4,
  reference: undefined,
  ai: undefined,
  aiOrigin: undefined,
})

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:open', value: boolean): void
    (e: 'closeAutoFocus', event: Event): void
  }
>()
</script>

<template>
  <PopoverRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <PopoverTrigger v-if="$slots.trigger" as-child>
      <slot name="trigger" />
    </PopoverTrigger>
    <PopoverAnchor v-else-if="reference" :reference="reference" />
    <PopoverPortal>
      <PopoverContent
        :side="side"
        :align="align"
        :side-offset="sideOffset"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-3 z-(--z-overlay) focus:outline-none max-w-xs data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out motion-reduce:data-[state=open]:animate-fade-in motion-reduce:data-[state=closed]:animate-fade-out origin-(--reka-popper-transform-origin)"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <PopupAnchor
          :ai="ai"
          :ai-origin="aiOrigin"
          @ai-origin-acknowledged="emit('aiOriginAcknowledged', $event)"
        />
        <slot />
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
