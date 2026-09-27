<script setup lang="ts">
import {
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
}

withDefaults(defineProps<UiPopoverProps>(), {
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'center',
  sideOffset: 4,
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
    <PopoverPortal>
      <PopoverContent
        :side="side"
        :align="align"
        :side-offset="sideOffset"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-3 z-50 focus:outline-none max-w-xs"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <slot />
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
