<script setup lang="ts">
import { ContextMenuItem } from 'reka-ui'
import UiKbd from '../kbd/UiKbd.vue'

export interface UiContextMenuItemProps {
  label?: string
  hint?: string
  kbd?: string
  disabled?: boolean
  textValue?: string
  asChild?: boolean
}

const props = withDefaults(defineProps<UiContextMenuItemProps>(), {
  label: undefined,
  hint: undefined,
  kbd: undefined,
  disabled: false,
  textValue: undefined,
  asChild: false,
})

const emit = defineEmits<{
  (e: 'select', event: Event): void
}>()
</script>

<template>
  <ContextMenuItem
    :disabled="disabled"
    :text-value="textValue ?? label"
    :as-child="asChild"
    class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
    @select="emit('select', $event)"
  >
    <slot v-if="asChild" />
    <template v-else>
      <slot>
        <span v-if="props.label" class="grow truncate">{{ props.label }}</span>
      </slot>
      <slot name="hint">
        <UiKbd v-if="props.hint || props.kbd" class="ml-auto">
          {{ props.hint || props.kbd }}
        </UiKbd>
      </slot>
    </template>
  </ContextMenuItem>
</template>
