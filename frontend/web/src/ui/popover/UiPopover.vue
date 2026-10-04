<script setup lang="ts">
import {
  PopoverAnchor,
  PopoverContent,
  PopoverPortal,
  PopoverRoot,
  PopoverTrigger,
} from 'reka-ui'

export interface UiPopoverProps {
  open?: boolean
  defaultOpen?: boolean
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  sideOffset?: number
  reference?: HTMLElement | { getBoundingClientRect: () => DOMRect } | null
}

withDefaults(defineProps<UiPopoverProps>(), {
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'center',
  sideOffset: 4,
  reference: undefined,
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
  (e: 'closeAutoFocus', event: Event): void
}>()
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
        <slot />
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
