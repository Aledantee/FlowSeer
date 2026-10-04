<script setup lang="ts">
import { computed, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AiRef, AiTarget } from '../../ai'
import { pageContext } from '../../navigation/page'

export interface UiAiEntityChipProps {
  entity?: { kind: string; id: string; label?: string } | AiRef | AiTarget
  target?: { kind: string; id: string; label?: string } | AiRef | AiTarget
  removable?: boolean
  size?: 'sm' | 'md'
}

const props = withDefaults(defineProps<UiAiEntityChipProps>(), {
  entity: undefined,
  target: undefined,
  removable: false,
  size: 'md',
})

const emit = defineEmits<{
  (e: 'remove'): void
}>()

const { t } = useI18n({ useScope: 'global' })
const page = inject(pageContext, null)

const item = computed(() => props.entity ?? props.target)
const labelText = computed(() => item.value?.label || item.value?.id || '')
const kind = computed(() => item.value?.kind)
const id = computed(() => item.value?.id)

const canNavigate = computed(() => {
  if (!page || !id.value) return false
  return kind.value === 'device' || kind.value === 'site'
})

const removeSymbol = '\u00D7'

function handleClick(event: MouseEvent) {
  if (!canNavigate.value || !page || !id.value) return
  event.preventDefault()
  if (kind.value === 'device') {
    void page.go({ path: `/devices/${id.value}` })
  } else if (kind.value === 'site') {
    void page.go({ path: '/dashboard', query: { site: id.value } })
  }
}
</script>

<template>
  <button
    v-if="canNavigate"
    type="button"
    class="inline-flex items-center gap-1 font-medium rounded-full bg-subtle text-foreground border border-border hover:bg-muted/80 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring cursor-pointer transition-colors whitespace-nowrap"
    :class="size === 'sm' ? 'h-5 px-2 text-2xs' : 'h-6 px-2.5 text-xs'"
    data-ai-entity-chip
    @click="handleClick"
  >
    <span translate="no">
      <slot>{{ labelText }}</slot>
    </span>
    <span
      v-if="removable"
      role="button"
      tabindex="0"
      :aria-label="t('ui.aiEntityChip.remove')"
      class="ml-0.5 text-muted-foreground hover:text-danger-foreground cursor-pointer leading-none"
      @click.stop="emit('remove')"
      @keydown.enter.stop="emit('remove')"
      @keydown.space.stop.prevent="emit('remove')"
      >{{ removeSymbol }}</span
    >
  </button>
  <span
    v-else
    class="inline-flex items-center gap-1 font-medium rounded-full bg-subtle text-foreground border border-border whitespace-nowrap"
    :class="size === 'sm' ? 'h-5 px-2 text-2xs' : 'h-6 px-2.5 text-xs'"
    data-ai-entity-chip
    translate="no"
  >
    <slot>{{ labelText }}</slot>
    <button
      v-if="removable"
      type="button"
      :aria-label="t('ui.aiEntityChip.remove')"
      class="ml-0.5 text-muted-foreground hover:text-danger-foreground cursor-pointer leading-none"
      @click.stop="emit('remove')"
    >
      {{ removeSymbol }}
    </button>
  </span>
</template>
