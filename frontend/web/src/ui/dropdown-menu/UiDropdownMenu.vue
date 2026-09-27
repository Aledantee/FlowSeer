<script setup lang="ts">
import {
  DropdownMenuContent,
  DropdownMenuPortal,
  DropdownMenuRoot,
  DropdownMenuTrigger,
} from 'reka-ui'

export interface UiDropdownMenuProps {
  open?: boolean
  defaultOpen?: boolean
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  sideOffset?: number
}

withDefaults(defineProps<UiDropdownMenuProps>(), {
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'start',
  sideOffset: 4,
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()
</script>

<template>
  <DropdownMenuRoot
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <DropdownMenuTrigger v-if="$slots.trigger" as-child>
      <slot name="trigger" />
    </DropdownMenuTrigger>
    <DropdownMenuPortal>
      <DropdownMenuContent
        :side="side"
        :align="align"
        :side-offset="sideOffset"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-50 min-w-[10rem] focus:outline-none"
      >
        <slot />
      </DropdownMenuContent>
    </DropdownMenuPortal>
  </DropdownMenuRoot>
</template>
