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
  <div class="scope-switcher">
    <UiCombobox
      :model-value="selected"
      :options="options"
      :placeholder="placeholder ?? 'Search…'"
      @select="emit('change', $event)"
    >
      <template #trigger>
        <button
          class="scope-trigger"
          :title="name"
          :aria-label="`${label}: ${name}`"
          aria-haspopup="listbox"
        >
          <slot
            ><span>{{ name }}</span></slot
          >
          <AppIcon class="scope-chevron" name="chevron" />
        </button>
      </template>
      <template #item="{ option, selected: isSelected }">
        <div
          class="flex items-center gap-2 w-full"
          :class="{ 'pl-4': option.nested }"
        >
          <img
            v-if="option.iconUrl"
            class="scope-avatar"
            :src="option.iconUrl"
            alt=""
          />
          <span class="flex-1 truncate">{{ option.label }}</span>
          <AppIcon v-if="isSelected" class="scope-check" name="check" />
        </div>
      </template>
    </UiCombobox>
  </div>
</template>
