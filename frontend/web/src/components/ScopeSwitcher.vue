<script setup lang="ts">
import { computed } from 'vue'
import AppIcon from './AppIcon.vue'
import { UiCombobox } from '../ui'

export interface ScopeOption {
  value: string
  label: string
  // Hierarchy depth, drawn as an indent while the list is unfiltered.
  nested?: boolean
  iconUrl?: string
}

const props = defineProps<{
  label: string
  selected: string
  options: ScopeOption[]
  placeholder?: string
}>()

const emit = defineEmits<{ change: [value: string] }>()

const name = computed(
  () =>
    props.options.find((option) => option.value === props.selected)?.label ??
    'Unavailable selection',
)
</script>

<template>
  <div class="scope-switcher min-w-0 w-full">
    <UiCombobox
      :model-value="selected"
      :options="options"
      :placeholder="placeholder ?? 'Search…'"
      @select="emit('change', $event)"
    >
      <template #trigger>
        <button
          class="scope-trigger group flex items-center gap-3 w-full px-1 py-1.5 border-0 bg-transparent text-chrome-foreground text-left text-xs rounded hover:bg-chrome-hover/45 aria-expanded:bg-chrome-hover/45 cursor-pointer"
          :title="name"
          :aria-label="`${label}: ${name}`"
          aria-haspopup="listbox"
        >
          <slot
            ><span class="min-w-0 truncate">{{ name }}</span></slot
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
          <span class="flex-1 truncate">{{ option.label }}</span>
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
