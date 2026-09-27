<script setup lang="ts">
import { ref, watch } from 'vue'
import { AiStaleError, AiUnavailableError } from '../../ai'
import type { AiTarget } from '../../ai'
import UiButton from '../button/UiButton.vue'
import { useAiRegistry } from './context'

// An on-demand summary. It makes no request until the operator activates
// Generate summary, so a page with several placements stays quiet until one
// is wanted. A retry is a fresh request, with its own request id.
const props = defineProps<{ target: AiTarget }>()
const registry = useAiRegistry()

type SummaryState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'result'; answer: string }
  | { kind: 'error'; message: string }
  | { kind: 'unavailable' }
const state = ref<SummaryState>({ kind: 'idle' })

// A newer request supersedes an older one; a late answer from the older
// request is dropped.
let token = 0

function targetSnapshot(target: AiTarget): string {
  return JSON.stringify([
    target.id,
    target.kind,
    target.label,
    target.segment,
    Object.entries(target.context).sort(([a], [b]) => a.localeCompare(b)),
  ])
}

watch(
  () => targetSnapshot(props.target),
  () => {
    token += 1
    state.value = { kind: 'idle' }
  },
  { flush: 'sync' },
)

async function generate() {
  const requestToken = (token += 1)
  const snapshot = targetSnapshot(props.target)
  state.value = { kind: 'loading' }
  try {
    const answer = await registry.request(props.target, { kind: 'summary' })
    if (requestToken !== token || snapshot !== targetSnapshot(props.target))
      return
    state.value = { kind: 'result', answer }
  } catch (error: unknown) {
    if (requestToken !== token || snapshot !== targetSnapshot(props.target))
      return
    if (error instanceof AiStaleError) {
      state.value = { kind: 'idle' }
      return
    }
    if (error instanceof AiUnavailableError) {
      state.value = { kind: 'unavailable' }
      return
    }
    state.value = {
      kind: 'error',
      message: error instanceof Error ? error.message : 'Something went wrong.',
    }
  }
}
function start() {
  void generate()
}
</script>

<template>
  <section
    class="flex flex-col items-start gap-2"
    :aria-label="`AI summary for ${target.label}`"
  >
    <UiButton
      v-if="state.kind === 'idle'"
      size="sm"
      variant="secondary"
      @click="start"
    >
      Generate summary
    </UiButton>

    <template v-else-if="state.kind === 'loading'">
      <div class="flex w-full flex-col gap-1.5" data-ai-summary-loading>
        <span
          class="ai-shimmer h-3 w-3/4 rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-full rounded-control"
          aria-hidden="true"
        ></span>
        <span
          class="ai-shimmer h-3 w-2/3 rounded-control"
          aria-hidden="true"
        ></span>
      </div>
      <span
        role="status"
        class="sr-only text-2xs text-muted-foreground motion-reduce:not-sr-only"
        >Generating summary…</span
      >
    </template>

    <p
      v-else-if="state.kind === 'result'"
      role="status"
      class="text-xs whitespace-pre-wrap text-foreground"
    >
      {{ state.answer }}
    </p>

    <template v-else-if="state.kind === 'unavailable'">
      <p role="status" class="text-xs text-muted-foreground">
        AI is unavailable
      </p>
      <UiButton size="sm" variant="ghost" @click="start">Retry</UiButton>
    </template>

    <template v-else>
      <p role="alert" class="text-xs text-danger-foreground">
        {{ state.message }}
      </p>
      <UiButton size="sm" variant="secondary" @click="start">Retry</UiButton>
    </template>
  </section>
</template>
