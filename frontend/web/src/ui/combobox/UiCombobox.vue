<script setup lang="ts">
import type { ComboboxRootEmits } from 'reka-ui'
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

const emptyOptionValue = computed(() => {
  let value = '__ui_combobox_empty__'
  const publicValues = new Set(props.options.map((option) => option.value))

  while (publicValues.has(value)) value += '_'

  return value
})

const groupedOptions = computed(() => {
  const groups = new Map<string | undefined, ComboboxOption[]>()
  for (const opt of props.options) {
    const list = groups.get(opt.group) || []
    list.push(opt)
    groups.set(opt.group, list)
  }
  return groups
})

const internalModelValue = computed(() => {
  if (Array.isArray(props.modelValue)) {
    return props.modelValue.map(toInternalValue)
  }
  if (typeof props.modelValue === 'string')
    return toInternalValue(props.modelValue)
  return undefined
})

const selectedValues = computed<ReadonlySet<string>>(() => {
  if (Array.isArray(props.modelValue)) return new Set(props.modelValue)
  if (typeof props.modelValue === 'string') return new Set([props.modelValue])
  return new Set()
})

function toInternalValue(value: string): string {
  return value === '' ? emptyOptionValue.value : value
}

function toPublicValue(value: string): string {
  return value === emptyOptionValue.value ? '' : value
}

function updateModelValue(value: unknown): void {
  if (Array.isArray(value) && value.every((item) => typeof item === 'string')) {
    emit('update:modelValue', value.map(toPublicValue))
    return
  }
  if (typeof value === 'string') emit('update:modelValue', toPublicValue(value))
}

function updateHighlight(
  item: ComboboxRootEmits<string>['highlight'][0],
): void {
  emit(
    'highlight',
    item ? { ...item, value: toPublicValue(item.value) } : undefined,
  )
}

function isSelected(value: string): boolean {
  return selectedValues.value.has(value)
}
</script>

<template>
  <ComboboxRoot
    :model-value="internalModelValue"
    :multiple="Array.isArray(modelValue)"
    :open="open"
    :default-open="defaultOpen"
    :ignore-filter="ignoreFilter"
    class="relative"
    @update:model-value="updateModelValue"
    @update:open="emit('update:open', $event)"
    @highlight="updateHighlight"
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
        <div v-if="$slots.trigger" class="p-1 border-b border-border mb-1">
          <ComboboxInput
            :placeholder="placeholder"
            class="flex h-8 w-full rounded-xs border border-border bg-input px-2 py-1 text-xs shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 text-foreground"
          />
        </div>
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
                :value="toInternalValue(opt.value)"
                :disabled="opt.disabled"
                class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
                @select="emit('select', opt.value)"
              >
                <slot
                  name="item"
                  :option="opt"
                  :selected="isSelected(opt.value)"
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
                :value="toInternalValue(opt.value)"
                :disabled="opt.disabled"
                class="relative flex cursor-pointer select-none items-center gap-2 rounded-sm px-2 py-1.5 text-sm outline-none data-[highlighted]:bg-hover data-[disabled]:pointer-events-none data-[disabled]:opacity-50 text-foreground"
                @select="emit('select', opt.value)"
              >
                <slot
                  name="item"
                  :option="opt"
                  :selected="isSelected(opt.value)"
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
