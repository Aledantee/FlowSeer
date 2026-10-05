<script setup lang="ts">
import {
  ContextMenuContent,
  ContextMenuPortal,
  ContextMenuRoot,
  ContextMenuTrigger,
} from 'reka-ui'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { PopupAnchor } from '../popover/popupAnchor'

export interface UiContextMenuProps extends UiAiProps {
  modal?: boolean
  dir?: 'ltr' | 'rtl'
  disabled?: boolean
}

withDefaults(defineProps<UiContextMenuProps>(), {
  modal: true,
  dir: undefined,
  disabled: false,
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
  <ContextMenuRoot
    :modal="modal"
    :dir="dir"
    @update:open="emit('update:open', $event)"
  >
    <ContextMenuTrigger v-if="$slots.trigger" as-child :disabled="disabled">
      <slot name="trigger" />
    </ContextMenuTrigger>
    <ContextMenuTrigger v-else as-child :disabled="disabled">
      <slot />
    </ContextMenuTrigger>

    <ContextMenuPortal>
      <ContextMenuContent
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-(--z-overlay) min-w-[10rem] focus:outline-none data-[state=open]:animate-overlay-in data-[state=closed]:animate-overlay-out motion-reduce:data-[state=open]:animate-fade-in motion-reduce:data-[state=closed]:animate-fade-out origin-(--reka-popper-transform-origin)"
        @close-auto-focus="emit('closeAutoFocus', $event)"
      >
        <PopupAnchor
          :ai="ai"
          :ai-origin="aiOrigin"
          @ai-origin-acknowledged="emit('aiOriginAcknowledged', $event)"
        />
        <slot v-if="$slots.trigger" />
        <slot v-else name="content" />
      </ContextMenuContent>
    </ContextMenuPortal>
  </ContextMenuRoot>
</template>
