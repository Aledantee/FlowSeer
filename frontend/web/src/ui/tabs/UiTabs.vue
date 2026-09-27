<script setup lang="ts">
import { TabsContent, TabsList, TabsRoot, TabsTrigger } from 'reka-ui'
import { tv } from 'tailwind-variants'

export interface TabItem {
  value: string
  label: string
  disabled?: boolean
  content?: string
}

export interface UiTabsProps {
  modelValue?: string
  defaultValue?: string
  orientation?: 'horizontal' | 'vertical'
  tabs?: TabItem[]
}

withDefaults(defineProps<UiTabsProps>(), {
  modelValue: undefined,
  defaultValue: undefined,
  orientation: 'horizontal',
  tabs: () => [],
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const triggerVariants = tv({
  base: 'inline-flex items-center justify-center whitespace-nowrap rounded-sm px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50',
  variants: {
    state: {
      active: 'bg-card text-foreground shadow-xs',
      inactive: 'text-muted-foreground hover:text-foreground',
    },
  },
})
</script>

<template>
  <TabsRoot
    :model-value="modelValue"
    :default-value="defaultValue"
    :orientation="orientation"
    class="flex flex-col"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <TabsList
      v-if="tabs && tabs.length"
      class="inline-flex items-center justify-center rounded-control bg-subtle p-1 text-muted-foreground border border-border self-start"
    >
      <TabsTrigger
        v-for="tab in tabs"
        :key="tab.value"
        :value="tab.value"
        :disabled="tab.disabled"
        :class="triggerVariants()"
        class="data-[state=active]:bg-card data-[state=active]:text-foreground data-[state=active]:shadow-xs data-[state=inactive]:text-muted-foreground data-[state=inactive]:hover:text-foreground"
      >
        <slot :name="`trigger-${tab.value}`" :tab="tab">{{ tab.label }}</slot>
      </TabsTrigger>
    </TabsList>
    <template v-if="tabs && tabs.length">
      <TabsContent
        v-for="tab in tabs"
        :key="tab.value"
        :value="tab.value"
        class="mt-2 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        <slot :name="tab.value" :tab="tab">
          {{ tab.content }}
        </slot>
      </TabsContent>
    </template>
    <slot />
  </TabsRoot>
</template>
