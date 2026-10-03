<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppIcon from './AppIcon.vue'
import { UiCombobox } from '../ui'

export interface ScopeOption {
  value: string
  label: string
  // Hierarchy depth, drawn as an indent while the list is unfiltered.
  nested?: boolean
  iconUrl?: string
  // The label is a name from the data, which a page translator leaves alone.
  identifier?: boolean
}

const props = defineProps<{
  label: string
  selected: string
  options: ScopeOption[]
  placeholder?: string
}>()

const emit = defineEmits<{ change: [value: string] }>()

const { t } = useI18n({ useScope: 'global' })

const selectedOption = computed(() =>
  props.options.find((option) => option.value === props.selected),
)
const name = computed(
  () => selectedOption.value?.label ?? t('view.scopeSwitcher.unavailable'),
)
const isIdentifier = (value: string) =>
  props.options.some((option) => option.value === value && option.identifier)
</script>

<template>
  <div class="scope-switcher min-w-0 w-full">
    <UiCombobox
      :model-value="selected"
      :options="options"
      :placeholder="placeholder ?? t('view.scopeSwitcher.searchPlaceholder')"
      @select="emit('change', $event)"
    >
      <template #trigger>
        <button
          class="scope-trigger group flex items-center gap-3 w-full px-1 py-1.5 border-0 bg-transparent text-chrome-foreground text-left text-xs rounded hover:bg-chrome-hover/45 aria-expanded:bg-chrome-hover/45 cursor-pointer"
          :title="name"
          :aria-label="t('view.scopeSwitcher.triggerLabel', { label, name })"
          aria-haspopup="listbox"
        >
          <slot
            ><span
              class="min-w-0 truncate"
              :translate="selectedOption?.identifier ? 'no' : undefined"
              >{{ name }}</span
            ></slot
          >
          <AppIcon
            class="scope-chevron w-3.5 h-3.5 shrink-0 ml-auto text-chrome-muted-foreground transition-transform duration-120 group-aria-expanded:rotate-180"
            name="chevron"
          />
        </button>
      </template>
      <template #item="{ option, selected: isSelected }">
        <div
          class="flex items-center gap-2 w-full"
          :class="{ 'pl-4': option.nested }"
        >
          <img
            v-if="option.iconUrl"
            class="scope-avatar w-4 h-4 rounded"
            :src="option.iconUrl"
            alt=""
          />
          <span
            class="flex-1 truncate"
            :translate="isIdentifier(option.value) ? 'no' : undefined"
            >{{ option.label }}</span
          >
          <AppIcon
            v-if="isSelected"
            class="scope-check w-3.5 h-3.5 shrink-0 ml-auto text-chrome-ring"
            name="check"
          />
        </div>
      </template>
    </UiCombobox>
  </div>
</template>
