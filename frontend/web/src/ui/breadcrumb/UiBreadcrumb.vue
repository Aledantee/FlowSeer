<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import UiBreadcrumbList from './UiBreadcrumbList.vue'
import UiBreadcrumbItem from './UiBreadcrumbItem.vue'
import UiBreadcrumbLink from './UiBreadcrumbLink.vue'
import UiBreadcrumbPage from './UiBreadcrumbPage.vue'
import UiBreadcrumbSeparator from './UiBreadcrumbSeparator.vue'
import UiBreadcrumbEllipsis from './UiBreadcrumbEllipsis.vue'

export interface BreadcrumbItemData {
  label: string
  href?: string
  to?: unknown
  current?: boolean
}

export interface UiBreadcrumbProps {
  items?: BreadcrumbItemData[]
  collapsed?: boolean
  collapseWidth?: number
  ariaLabel?: string
}

const props = withDefaults(defineProps<UiBreadcrumbProps>(), {
  items: () => [],
  collapsed: undefined,
  collapseWidth: 380,
  ariaLabel: undefined,
})

const { t } = useI18n({ useScope: 'global' })
const resolvedAriaLabel = computed(
  () => props.ariaLabel ?? t('ui.breadcrumb.ariaLabel'),
)

const navRef = ref<HTMLElement | null>(null)
const autoCollapsed = ref(false)

let resizeObserver: ResizeObserver | null = null

onMounted(() => {
  if (typeof ResizeObserver === 'undefined' || !navRef.value) return
  resizeObserver = new ResizeObserver((entries) => {
    for (const entry of entries) {
      if (
        entry.contentRect.width > 0 &&
        entry.contentRect.width < props.collapseWidth
      ) {
        autoCollapsed.value = true
      } else {
        autoCollapsed.value = false
      }
    }
  })
  resizeObserver.observe(navRef.value)
})

onUnmounted(() => {
  resizeObserver?.disconnect()
})

const isCollapsed = computed(() => {
  if (props.collapsed !== undefined) return props.collapsed
  return autoCollapsed.value
})

provide('ui-breadcrumb-collapsed', isCollapsed)

const firstItem = computed(() => props.items[0])
const lastItem = computed(() => props.items[props.items.length - 1])
const middleItems = computed(() =>
  props.items.length > 2 ? props.items.slice(1, props.items.length - 1) : [],
)
</script>

<template>
  <nav ref="navRef" :aria-label="resolvedAriaLabel">
    <slot :collapsed="isCollapsed">
      <UiBreadcrumbList v-if="items && items.length">
        <template v-if="isCollapsed && items.length > 2">
          <UiBreadcrumbItem>
            <UiBreadcrumbLink :href="firstItem?.href">
              {{ firstItem?.label }}
            </UiBreadcrumbLink>
          </UiBreadcrumbItem>
          <UiBreadcrumbSeparator />

          <UiBreadcrumbItem>
            <UiBreadcrumbEllipsis :items="middleItems" />
          </UiBreadcrumbItem>
          <UiBreadcrumbSeparator />

          <UiBreadcrumbItem>
            <UiBreadcrumbPage>{{ lastItem?.label }}</UiBreadcrumbPage>
          </UiBreadcrumbItem>
        </template>

        <template v-else>
          <template v-for="(item, idx) in items" :key="item.label">
            <UiBreadcrumbItem>
              <UiBreadcrumbPage v-if="idx === items.length - 1 || item.current">
                {{ item.label }}
              </UiBreadcrumbPage>
              <UiBreadcrumbLink v-else :href="item.href">
                {{ item.label }}
              </UiBreadcrumbLink>
            </UiBreadcrumbItem>
            <UiBreadcrumbSeparator v-if="idx < items.length - 1" />
          </template>
        </template>
      </UiBreadcrumbList>
    </slot>
  </nav>
</template>
