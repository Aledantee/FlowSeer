<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import UiDropdownMenu from '../dropdown-menu/UiDropdownMenu.vue'
import UiDropdownMenuItem from '../dropdown-menu/UiDropdownMenuItem.vue'

export interface BreadcrumbEllipsisItem {
  label: string
  href?: string
  to?: unknown
}

export interface UiBreadcrumbEllipsisProps {
  items?: BreadcrumbEllipsisItem[]
  toggleLabel?: string
}

const props = defineProps<UiBreadcrumbEllipsisProps>()

const { t } = useI18n({ useScope: 'global' })
const resolvedToggleLabel = computed(
  () => props.toggleLabel ?? t('ui.breadcrumbEllipsis.toggleLabel'),
)
</script>

<template>
  <UiDropdownMenu align="start">
    <template #trigger>
      <button
        type="button"
        :aria-label="resolvedToggleLabel"
        class="flex h-9 w-9 items-center justify-center rounded-control hover:bg-hover text-muted-foreground hover:text-foreground transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        <span
          role="presentation"
          aria-hidden="true"
          class="flex h-9 w-9 items-center justify-center"
        >
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="12" cy="12" r="1" />
            <circle cx="19" cy="12" r="1" />
            <circle cx="5" cy="12" r="1" />
          </svg>
        </span>
      </button>
    </template>
    <slot name="menu">
      <slot>
        <template v-if="items && items.length">
          <UiDropdownMenuItem v-for="item in items" :key="item.label" as-child>
            <a :href="item.href || '#'" class="w-full">
              {{ item.label }}
            </a>
          </UiDropdownMenuItem>
        </template>
      </slot>
    </slot>
  </UiDropdownMenu>
</template>
