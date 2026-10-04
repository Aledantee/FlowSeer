<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AiRef, AiRequest, AiRun } from '../../ai'
import UiPopover from '../popover/UiPopover.vue'
import UiAiEntityChip from './UiAiEntityChip.vue'

export interface UiAiLabelProps {
  request?: Omit<AiRequest, 'signal'>
  run?: AiRun
  sources?: (AiRef | string)[]
  open?: boolean
  defaultOpen?: boolean
}

const props = withDefaults(defineProps<UiAiLabelProps>(), {
  request: undefined,
  run: undefined,
  sources: () => [],
  open: undefined,
  defaultOpen: false,
})

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const { t } = useI18n({ useScope: 'global' })

const req = computed(() => props.request ?? props.run?.request)
const targets = computed(() => req.value?.targets ?? [])
const allSources = computed(() => props.sources ?? [])

function formatValue(v: unknown): string {
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return JSON.stringify(v)
}

function hasContext(target: { context?: Record<string, unknown> }): boolean {
  return Boolean(target.context && Object.keys(target.context).length > 0)
}

function getContextEntries(target: {
  context?: Record<string, unknown>
}): [string, unknown][] {
  if (!target.context) return []
  return Object.entries(target.context).sort(([a], [b]) => a.localeCompare(b))
}

function getTargetSegment(target: unknown): string | undefined {
  return (target as { segment?: string })?.segment
}

function formatSegment(seg?: string): string {
  return seg ? `(${seg})` : ''
}
</script>

<template>
  <UiPopover
    :open="open"
    :default-open="defaultOpen"
    @update:open="emit('update:open', $event)"
  >
    <template #trigger>
      <button
        type="button"
        class="inline-flex items-center justify-center font-medium rounded-control px-1.5 py-0.5 text-2xs bg-subtle text-muted-foreground hover:text-foreground border border-border cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        :aria-label="t('ui.aiLabel.ariaLabel')"
        data-ai-label-trigger
      >
        {{ t('ui.aiLabel.badge') }}
      </button>
    </template>
    <div class="flex flex-col gap-2 text-xs" data-ai-label-content>
      <div class="font-medium text-foreground border-b border-border pb-1">
        {{ t('ui.aiLabel.title') }}
      </div>
      <div v-if="targets.length > 0" class="flex flex-col gap-2">
        <div
          v-for="target in targets"
          :key="target.id"
          class="flex flex-col gap-0.5"
          data-ai-label-target
        >
          <div
            class="font-medium text-2xs text-muted-foreground flex items-center gap-1"
          >
            <span>{{ target.label }}</span>
            <span
              v-if="getTargetSegment(target)"
              class="text-3xs bg-muted px-1 rounded"
              >{{ formatSegment(getTargetSegment(target)) }}</span
            >
          </div>
          <dl
            v-if="hasContext(target)"
            class="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-2xs"
            data-ai-label-context-list
          >
            <template v-for="[k, v] in getContextEntries(target)" :key="k">
              <dt class="text-muted-foreground">{{ `${k}:` }}</dt>
              <dd class="font-mono text-foreground break-all">
                {{ formatValue(v) }}
              </dd>
            </template>
          </dl>
        </div>
      </div>
      <div
        v-if="allSources.length > 0"
        class="flex flex-col gap-1 border-t border-border pt-1.5"
      >
        <span class="text-2xs font-medium text-muted-foreground">{{
          t('ui.aiLabel.sources')
        }}</span>
        <div class="flex flex-wrap gap-1">
          <template
            v-for="src in allSources"
            :key="typeof src === 'string' ? src : src.id"
          >
            <UiAiEntityChip
              v-if="typeof src !== 'string'"
              :entity="src"
              size="sm"
            />
            <span v-else class="text-2xs font-mono bg-subtle px-1 rounded">{{
              src
            }}</span>
          </template>
        </div>
      </div>
      <p
        class="text-2xs text-muted-foreground border-t border-border pt-1.5 italic"
      >
        {{ t('ui.aiLabel.disclaimer') }}
      </p>
    </div>
  </UiPopover>
</template>
