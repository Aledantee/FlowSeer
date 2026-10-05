<script setup lang="ts">
import { computed, h, ref, shallowRef, useTemplateRef } from 'vue'
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
  ComboboxViewport,
  injectComboboxRootContext,
  Primitive,
  useId,
} from 'reka-ui'
import { useI18n } from 'vue-i18n'
import type { UiAiEmits, UiAiProps } from '../ai/context'
import { useAiOrigin } from '../ai/useAiOrigin'
import { useAiTarget } from '../ai/useAiTarget'

export interface ComboboxOption {
  value: string
  label: string
  disabled?: boolean
  group?: string
  nested?: boolean
  iconUrl?: string
}

export interface UiComboboxProps extends UiAiProps {
  modelValue?: string | string[]
  options?: ComboboxOption[]
  ignoreFilter?: boolean
  placeholder?: string
  emptyText?: string
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
  placeholder: undefined,
  emptyText: undefined,
  open: undefined,
  defaultOpen: false,
  side: 'bottom',
  align: 'start',
  sideOffset: 4,
  ai: undefined,
  aiOrigin: undefined,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedPlaceholder = computed(
  () => props.placeholder ?? t('ui.combobox.placeholder'),
)
const resolvedEmptyText = computed(
  () => props.emptyText ?? t('ui.combobox.emptyText'),
)

const emit = defineEmits<
  UiAiEmits & {
    (e: 'update:modelValue', value: string | string[]): void
    (e: 'update:open', value: boolean): void
    (e: 'highlight', item: unknown): void
    (e: 'select', value: string): void
  }
>()

// The value's control is the input, or the caller's trigger element when the
// input moves into the popup. A caller-supplied input slot owns its element.
const input = useTemplateRef('input')
const trigger = shallowRef<HTMLElement | null>(null)
function setTrigger(element: HTMLElement | null) {
  trigger.value = element
}
const anchor = () => trigger.value ?? input.value
useAiTarget(anchor, () => props.ai)
// The options and the popup input live in a portal, outside the anchor.
const aiOrigin = useAiOrigin(
  anchor,
  () => props.aiOrigin,
  (requestId) => emit('aiOriginAcknowledged', requestId),
)

const emptyOptionValue = computed(() => {
  let value = '__ui_combobox_empty__'
  const publicValues = new Set(props.options.map((option) => option.value))

  while (publicValues.has(value)) value += '_'

  return value
})

function toInternalValue(val: string): string {
  return val === '' ? emptyOptionValue.value : val
}

function toPublicValue(val: string): string {
  return val === emptyOptionValue.value ? '' : val
}

function displayValue(value: unknown): string {
  if (typeof value !== 'string') return ''

  const publicValue = toPublicValue(value)
  return (
    props.options.find((option) => option.value === publicValue)?.label ??
    publicValue
  )
}

const highlightedValue = ref<string | null>(null)

function onHighlight(item: unknown) {
  let publicVal: string | null = null
  let emittedItem = item
  if (
    item &&
    typeof item === 'object' &&
    'value' in item &&
    typeof (item as { value: unknown }).value === 'string'
  ) {
    publicVal = toPublicValue((item as { value: string }).value)
    emittedItem = { ...item, value: publicVal }
  } else if (typeof item === 'string') {
    publicVal = toPublicValue(item)
    emittedItem = publicVal
  }
  highlightedValue.value = publicVal
  emit('highlight', emittedItem)
}

function onOpenUpdate(val: boolean) {
  if (!val) {
    highlightedValue.value = null
  }
  emit('update:open', val)
}

function isOptionActive(val: string): boolean {
  return highlightedValue.value !== null && highlightedValue.value === val
}

const internalModelValue = computed(() => {
  if (props.modelValue === undefined) return undefined
  if (Array.isArray(props.modelValue)) {
    return props.modelValue.map(toInternalValue)
  }
  return toInternalValue(props.modelValue)
})

function isOptionSelected(val: string): boolean {
  if (props.modelValue === undefined) return false
  if (Array.isArray(props.modelValue)) {
    return props.modelValue.includes(val)
  }
  return props.modelValue === val
}

const groupedOptions = computed(() => {
  const groups = new Map<string | undefined, ComboboxOption[]>()
  for (const opt of props.options) {
    const list = groups.get(opt.group) || []
    list.push(opt)
    groups.set(opt.group, list)
  }
  return groups
})

function UiCustomComboboxTrigger(
  props: {
    asChild?: boolean
    disabled?: boolean
    anchor?: (element: HTMLElement | null) => void
  },
  { slots }: { slots: { default?: () => unknown } },
) {
  const rootContext = injectComboboxRootContext()
  rootContext.contentId ||= useId(undefined, 'reka-combobox-content')

  const disabled = props.disabled || rootContext.disabled.value || false

  function onKeydown(event: KeyboardEvent) {
    if (disabled) return
    if (
      event.key === 'Enter' ||
      event.key === ' ' ||
      event.key === 'Spacebar'
    ) {
      event.preventDefault()
      rootContext.onOpenChange(!rootContext.open.value)
    }
  }

  function onClick() {
    if (disabled) return
    rootContext.onOpenChange(!rootContext.open.value)
  }

  return h(
    Primitive,
    {
      ref: (el: unknown) => {
        if (el && typeof el === 'object') {
          const domEl = '$el' in el ? (el as { $el: HTMLElement }).$el : el
          if (domEl instanceof HTMLElement) {
            rootContext.onTriggerElementChange(domEl)
            props.anchor?.(domEl)
          }
        } else {
          props.anchor?.(null)
        }
      },
      asChild: props.asChild ?? true,
      type: 'button',
      tabindex: 0,
      'aria-haspopup': 'listbox',
      'aria-expanded': rootContext.open.value,
      'aria-controls': rootContext.contentId,
      'data-state': rootContext.open.value ? 'open' : 'closed',
      disabled: disabled ? '' : undefined,
      'data-disabled': disabled ? '' : undefined,
      'aria-disabled': disabled || undefined,
      onClick,
      onKeydown,
    },
    slots,
  )
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
    @update:model-value="
      (val) => {
        if (Array.isArray(val)) {
          emit(
            'update:modelValue',
            val.map((v) =>
              typeof v === 'string' ? toPublicValue(v) : String(v),
            ),
          )
        } else if (typeof val === 'string') {
          emit('update:modelValue', toPublicValue(val))
        }
      }
    "
    @update:open="onOpenUpdate"
    @highlight="onHighlight"
  >
    <UiCustomComboboxTrigger
      v-if="$slots.trigger"
      as-child
      :anchor="setTrigger"
    >
      <slot name="trigger" />
    </UiCustomComboboxTrigger>
    <slot v-else name="input">
      <ComboboxInput
        ref="input"
        :placeholder="resolvedPlaceholder"
        :display-value="displayValue"
        class="flex h-9 w-full rounded-control border border-border bg-input px-3 py-1 text-sm shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 text-foreground"
      />
    </slot>
    <ComboboxPortal>
      <ComboboxContent
        position="popper"
        :side="side"
        :align="align"
        :side-offset="sideOffset"
        :collision-padding="8"
        class="bg-popover text-foreground border border-border shadow-lg rounded-control p-1 z-(--z-overlay) max-h-60 overflow-y-auto min-w-[8rem] focus:outline-none"
        @pointerdown.capture="aiOrigin.acknowledge"
        @keydown.capture="aiOrigin.acknowledge"
        @input.capture="aiOrigin.acknowledge"
      >
        <div v-if="$slots.trigger" class="p-1 border-b border-border mb-1">
          <ComboboxInput
            :placeholder="resolvedPlaceholder"
            :display-value="displayValue"
            class="flex h-8 w-full rounded-xs border border-border bg-input px-2 py-1 text-xs shadow-xs transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 text-foreground"
          />
        </div>
        <ComboboxViewport class="p-1">
          <ComboboxEmpty class="py-6 text-center text-sm text-muted-foreground">
            <slot name="empty">{{ resolvedEmptyText }}</slot>
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
                  :selected="isOptionSelected(opt.value)"
                  :active="isOptionActive(opt.value)"
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
                  :selected="isOptionSelected(opt.value)"
                  :active="isOptionActive(opt.value)"
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
