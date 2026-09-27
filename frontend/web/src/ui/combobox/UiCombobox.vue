<script setup lang="ts">
import { computed } from 'vue'
import {
  ComboboxContent,
  ComboboxEmpty,
  ComboboxGroup,
  ComboboxInput,
  ComboboxItem,
  ComboboxItemIndicator,
  ComboboxLabel,
  ComboboxPortal,
  ComboboxRoot,
  ComboboxTrigger,
  ComboboxViewport,
} from 'reka-ui'

export interface ComboboxOption {
  value: string
  label: string
  disabled?: boolean
  group?: string
  nested?: boolean
  iconUrl?: string
}

export interface UiComboboxProps {
  modelValue?: string | string[]
  options?: ComboboxOption[]
  ignoreFilter?: boolean
  placeholder?: string
  open?: boolean
  defaultOpen?: boolean
  side?: 'top' | 'right' | 'bottom' | 'left'
  align?: 'start' | 'center' | 'end'
  sideOffset?: number
}

const props = withDefaults(defineProps<UiComboboxProps>(), {
  modelValue: undefined,
  options: () => [],
  ignoreFilter: false,
  placeholder: 'Search...',
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'start',
  sideOffset: 4,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | string[]): void
  (e: 'update:open', value: boolean): void
  (e: 'highlight', item: unknown): void
  (e: 'select', value: string): void
}>()

const groupedOptions = computed(() => {
  const groups = new Map<string | undefined, ComboboxOption[]>()
  for (const opt of props.options) {
    const list = groups.get(opt.group) || []
    list.push(opt)
    groups.set(opt.group, list)
  }
  return groups
})
</script>

<template>
  <ComboboxRoot
    :model-value="modelValue"
    :open="open"
    :default-open="defaultOpen"
    :ignore-filter="ignoreFilter"
    class="relative"
    @update:model-value="
      (val) => {
        emit('update:modelValue', val as string | string[])
        if (typeof val === 'string') emit('select', val)
      }
    "
    @update:open="emit('update:open', $event)"
    @highlight="emit('highlight', $event)"
  >
    <ComboboxTrigger v-if="$slots.trigger" as-child>
      <slot name="trigger" />
    </ComboboxTrigger>
    <slot v-else name="input">
      <ComboboxInput
        :placeholder="placeholder"
        class="flex h-9 w-full rounded-control border border-border bg-input px-3 py-1 text-sm shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 text-foreground"
      />
    </slot>
    <ComboboxPortal>
      <ComboboxContent
        :side="side"
        :align="align"
        :side-offset="sideOffset"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-50 max-h-60 overflow-y-auto min-w-[8rem] focus:outline-none"
      >
        <ComboboxViewport class="p-1">
          <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
            <slot name="empty">No results found.</slot>
          </ComboboxEmpty>
          <template
            v-for="[groupName, opts] in groupedOptions"
            :key="groupName || 'default'"
          >
            <ComboboxGroup v-if="groupName">
              <ComboboxLabel
                class="px-2 py-1.5 text-xs font-semibold text-muted-foreground"
              >
                <slot name="group-header" :group="groupName">
                  {{ groupName }}
                </slot>
              </ComboboxLabel>
              <ComboboxItem
                v-for="opt in opts"
                :key="opt.value"
                :value="opt.value"
                :disabled="opt.disabled"
                class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
                @select="emit('select', opt.value)"
              >
                <slot
                  name="item"
                  :option="opt"
                  :selected="modelValue === opt.value"
                  :active="false"
                >
                  <ComboboxItemIndicator
                    class="inline-flex items-center justify-center"
                  >
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      aria-hidden="true"
                    >
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  </ComboboxItemIndicator>
                  <span>{{ opt.label }}</span>
                </slot>
              </ComboboxItem>
            </ComboboxGroup>
            <template v-else>
              <ComboboxItem
                v-for="opt in opts"
                :key="opt.value"
                :value="opt.value"
                :disabled="opt.disabled"
                class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
                @select="emit('select', opt.value)"
              >
                <slot
                  name="item"
                  :option="opt"
                  :selected="modelValue === opt.value"
                  :active="false"
                >
                  <ComboboxItemIndicator
                    class="inline-flex items-center justify-center"
                  >
                    <svg
                      width="14"
                      height="14"
                      viewBox="0 0 24 24"
                      fill="none"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                      aria-hidden="true"
                    >
                      <polyline points="20 6 9 17 4 12" />
                    </svg>
                  </ComboboxItemIndicator>
                  <span>{{ opt.label }}</span>
                </slot>
              </ComboboxItem>
            </template>
          </template>
        </ComboboxViewport>
      </ComboboxContent>
    </ComboboxPortal>
  </ComboboxRoot>
</template>
