<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  aiActions,
  AiUnavailableError,
  type AiRequest,
  type AiResult,
  type AiRun,
  type AiSeed,
  type AiTarget,
  type AiTargetSnapshot,
  type AiTurn,
} from '../../ai'
import { useAiRegistry } from './context'
import UiAiEntityChip from './UiAiEntityChip.vue'
import UiAiLabel from './UiAiLabel.vue'
import UiAiResult, { type UiAiResultState } from './UiAiResult.vue'
import UiAiResultActions from './UiAiResultActions.vue'
import UiButton from '../button/UiButton.vue'
import UiPopover from '../popover/UiPopover.vue'
import UiCommand from '../command/UiCommand.vue'
import UiCommandInput from '../command/UiCommandInput.vue'
import UiCommandList from '../command/UiCommandList.vue'
import UiCommandItem from '../command/UiCommandItem.vue'
import UiCommandEmpty from '../command/UiCommandEmpty.vue'
import UiTextarea from '../form/UiTextarea.vue'

export interface UiAiAssistantLabels {
  title?: string
  close?: string
  addContext?: string
  searchContext?: string
  noTargets?: string
  suggestions?: string
  inputPlaceholder?: string
  inputLabel?: string
  send?: string
  stop?: string
  unavailable?: string
  emptyThread?: string
}

export interface UiAiAssistantProps {
  open?: boolean
  seed?: AiSeed
  labels?: UiAiAssistantLabels
}

export interface UiAiAssistantTurnItem {
  role: 'user' | 'assistant'
  prompt?: string
  result?: AiResult | null
}

interface AssistantTurnMeta {
  run?: AiRun
  request?: Omit<AiRequest, 'signal'>
  state: UiAiResultState
  error?: string
  requestId?: string
}

const props = withDefaults(defineProps<UiAiAssistantProps>(), {
  open: true,
  seed: undefined,
  labels: undefined,
})

const emit = defineEmits<{
  (e: 'update:open', open: boolean): void
  (e: 'close'): void
}>()

const { t } = useI18n({ useScope: 'global' })
const registry = useAiRegistry()

const chips = ref<AiTargetSnapshot[]>([])
const turns = ref<UiAiAssistantTurnItem[]>([])
const turnMetaMap = ref<Map<number, AssistantTurnMeta>>(new Map())
const promptInput = ref('')
const isGenerating = ref(false)
const activeRun = ref<AiRun>()
const addContextOpen = ref(false)
const registeredTargets = ref<AiTarget[]>([])

const closeSymbol = '\u00D7'
const plusSymbol = '+'

function toSnapshot(target: AiTarget): AiTargetSnapshot {
  return {
    id: target.id,
    kind: target.kind,
    view: target.view ?? '',
    label: target.label,
    context: { ...target.context },
    ...(target.entity ? { entity: { ...target.entity } } : {}),
  }
}

function getTurnMeta(index: number): AssistantTurnMeta | undefined {
  return turnMetaMap.value.get(index)
}

function applySeed(seed?: AiSeed) {
  if (!seed) return
  chips.value = [...seed.targets]
  turns.value = [...seed.turns]
  const newMeta = new Map<number, AssistantTurnMeta>()
  turns.value.forEach((turn, idx) => {
    if (turn.role === 'assistant') {
      newMeta.set(idx, {
        state: 'done',
        request: {
          requestId: `seed-${idx}`,
          action: 'summary',
          targets: [...seed.targets],
          history: [],
        },
      })
    }
  })
  turnMetaMap.value = newMeta
}

watch(
  () => props.seed,
  (newSeed) => {
    applySeed(newSeed)
  },
  { immediate: true },
)

const availableTargets = computed(() => {
  return registeredTargets.value.filter(
    (target) => !chips.value.some((c) => c.id === target.id),
  )
})

const suggestions = computed(() => {
  if (turns.value.length > 0) return []
  const list: string[] = []
  const seen = new Set<string>()
  for (const chip of chips.value) {
    for (const action of aiActions(chip)) {
      if (action === 'Ask about this…') continue
      if (!seen.has(action)) {
        seen.add(action)
        list.push(action)
      }
    }
  }
  return list
})

function removeChip(index: number) {
  chips.value.splice(index, 1)
}

function handleAddTarget(target: AiTarget) {
  if (!chips.value.some((c) => c.id === target.id)) {
    chips.value.push(toSnapshot(target))
  }
  addContextOpen.value = false
}

function handleClose() {
  emit('update:open', false)
  emit('close')
}

function handleSelectSuggestion(suggestion: string) {
  void sendPrompt(suggestion)
}

async function sendPrompt(text: string) {
  const promptText = text.trim()
  if (!promptText || isGenerating.value) return

  promptInput.value = ''

  const history: AiTurn[] = turns.value.filter(
    (turn): turn is AiTurn =>
      turn.role === 'user' ||
      (turn.role === 'assistant' &&
        turn.result !== null &&
        turn.result !== undefined),
  )

  turns.value.push({ role: 'user', prompt: promptText })

  const assistantIndex = turns.value.length
  turns.value.push({
    role: 'assistant',
    result: null,
  })

  turnMetaMap.value.set(assistantIndex, {
    state: 'generating',
  })

  await executeRun(assistantIndex, promptText, history)
}

async function executeRun(
  turnIndex: number,
  promptText: string,
  history: AiTurn[],
) {
  isGenerating.value = true
  try {
    const run = registry.request(chips.value, {
      action: 'ask',
      prompt: promptText,
      history,
      bound: false,
    })
    activeRun.value = run

    const meta = turnMetaMap.value.get(turnIndex) ?? { state: 'generating' }
    meta.run = run
    meta.request = run.request
    meta.state = 'generating'
    meta.error = undefined
    meta.requestId = run.requestId
    turnMetaMap.value.set(turnIndex, meta)

    for await (const snapshot of run.snapshots) {
      const turn = turns.value[turnIndex]
      if (turn && turn.role === 'assistant') {
        turn.result = snapshot
      }
    }

    if (meta.state === 'generating') {
      meta.state = 'done'
    }
  } catch (err: unknown) {
    const meta = turnMetaMap.value.get(turnIndex) ?? { state: 'error' }
    if (err instanceof AiUnavailableError) {
      meta.state = 'unavailable'
      meta.error = t('ui.aiAssistant.unavailable')
    } else {
      meta.state = 'error'
      meta.error = err instanceof Error ? err.message : t('ui.aiResult.error')
    }
    turnMetaMap.value.set(turnIndex, meta)
  } finally {
    isGenerating.value = false
    activeRun.value = undefined
  }
}

function handleActiveStop() {
  activeRun.value?.stop()
  activeRun.value = undefined
  isGenerating.value = false
  for (const [idx, meta] of turnMetaMap.value.entries()) {
    if (meta.state === 'generating') {
      meta.state = 'stopped'
      turnMetaMap.value.set(idx, meta)
    }
  }
}

function handleStop(turnIndex: number) {
  const meta = turnMetaMap.value.get(turnIndex)
  meta?.run?.stop()
  if (meta) {
    meta.state = 'stopped'
    turnMetaMap.value.set(turnIndex, meta)
  }
  if (activeRun.value === meta?.run) {
    activeRun.value = undefined
    isGenerating.value = false
  }
}

function handleRegenerate(turnIndex: number) {
  if (isGenerating.value) return
  const userTurn = turns.value[turnIndex - 1]
  if (userTurn && userTurn.role === 'user' && userTurn.prompt) {
    const history: AiTurn[] = turns.value
      .slice(0, turnIndex - 1)
      .filter(
        (turn): turn is AiTurn =>
          turn.role === 'user' ||
          (turn.role === 'assistant' &&
            turn.result !== null &&
            turn.result !== undefined),
      )
    void executeRun(turnIndex, userTurn.prompt, history)
  }
}

function handleSend() {
  if (!isGenerating.value && promptInput.value.trim()) {
    void sendPrompt(promptInput.value)
  }
}

function handleKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && !event.shiftKey) {
    event.preventDefault()
    handleSend()
  }
}

let unsubscribe = () => {}

onMounted(() => {
  registeredTargets.value = registry.list()
  unsubscribe = registry.subscribe(() => {
    registeredTargets.value = registry.list()
  })
})

onUnmounted(() => {
  unsubscribe()
  activeRun.value?.stop()
})
</script>

<template>
  <section
    v-if="open"
    class="flex flex-col h-full bg-card w-full"
    :aria-label="props.labels?.title ?? t('ui.aiAssistant.title')"
    data-ai-assistant
  >
    <header
      class="flex items-center justify-between px-4 py-3 border-b border-border bg-card shrink-0"
    >
      <h2 class="text-sm font-semibold text-foreground">
        {{ props.labels?.title ?? t('ui.aiAssistant.title') }}
      </h2>
      <button
        type="button"
        class="text-muted-foreground hover:text-foreground text-sm p-1 rounded-sm cursor-pointer transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        :aria-label="props.labels?.close ?? t('ui.aiAssistant.close')"
        @click="handleClose"
      >
        {{ closeSymbol }}
      </button>
    </header>

    <div
      class="flex items-center gap-1.5 px-4 py-2 border-b border-border bg-muted/20 overflow-x-auto flex-wrap shrink-0"
      data-ai-assistant-chips
    >
      <UiAiEntityChip
        v-for="(chip, index) in chips"
        :key="chip.id"
        :target="chip"
        size="sm"
        removable
        @remove="removeChip(index)"
      />
      <UiPopover v-model:open="addContextOpen">
        <template #trigger>
          <UiButton
            size="sm"
            variant="ghost"
            class="gap-1 rounded-full text-2xs h-5 px-2 border border-dashed border-border hover:border-foreground/40 text-muted-foreground hover:text-foreground cursor-pointer"
            data-ai-assistant-add-context
          >
            <span>{{ plusSymbol }}</span>
            <span>{{
              props.labels?.addContext ?? t('ui.aiAssistant.addContext')
            }}</span>
          </UiButton>
        </template>
        <div class="w-64 p-1">
          <UiCommand>
            <UiCommandInput
              :placeholder="
                props.labels?.searchContext ?? t('ui.aiAssistant.searchContext')
              "
            />
            <UiCommandList>
              <UiCommandEmpty
                :text="props.labels?.noTargets ?? t('ui.aiAssistant.noTargets')"
              />
              <UiCommandItem
                v-for="target in availableTargets"
                :key="target.id"
                :value="target.label || target.id"
                @select="handleAddTarget(target)"
              >
                <span>{{ target.label || target.id }}</span>
              </UiCommandItem>
            </UiCommandList>
          </UiCommand>
        </div>
      </UiPopover>
    </div>

    <div class="flex-1 overflow-y-auto p-4 space-y-4" data-ai-assistant-thread>
      <div
        v-if="turns.length === 0"
        class="flex flex-col gap-3 py-6"
        data-ai-assistant-empty
      >
        <div v-if="suggestions.length > 0" class="flex flex-col gap-2">
          <span class="text-xs font-medium text-muted-foreground">
            {{ props.labels?.suggestions ?? t('ui.aiAssistant.suggestions') }}
          </span>
          <div class="flex flex-col gap-1.5 items-start">
            <button
              v-for="s in suggestions"
              :key="s"
              type="button"
              class="text-left text-xs px-3 py-1.5 rounded-full border border-border bg-subtle hover:bg-muted/80 text-foreground transition-colors cursor-pointer"
              data-ai-assistant-suggestion
              @click="handleSelectSuggestion(s)"
            >
              {{ s }}
            </button>
          </div>
        </div>
        <p v-else class="text-xs text-muted-foreground text-center py-4">
          {{ props.labels?.emptyThread ?? t('ui.aiAssistant.emptyThread') }}
        </p>
      </div>

      <template v-for="(turn, index) in turns" :key="index">
        <div v-if="turn.role === 'user'" class="flex justify-end">
          <div
            class="bg-muted text-foreground rounded-panel px-3 py-2 text-xs max-w-[85%] whitespace-pre-wrap"
            data-ai-assistant-user-turn
          >
            {{ turn.prompt }}
          </div>
        </div>

        <div
          v-else-if="turn.role === 'assistant'"
          class="flex flex-col gap-2 p-3 rounded-panel border border-border bg-card"
          data-ai-assistant-turn
        >
          <div
            class="flex items-center justify-between pb-1 border-b border-border"
          >
            <UiAiLabel
              :request="getTurnMeta(index)?.request"
              :run="getTurnMeta(index)?.run"
              :sources="
                turn.result?.type === 'summary' ? turn.result.sources : []
              "
            />
          </div>
          <UiAiResult
            :run="getTurnMeta(index)?.run"
            :result="turn.result"
            :state="getTurnMeta(index)?.state"
            :error="getTurnMeta(index)?.error"
            @stop="handleStop(index)"
          />
          <div
            v-if="turn.result && getTurnMeta(index)?.state !== 'generating'"
            class="flex items-center justify-end pt-1 border-t border-border"
          >
            <UiAiResultActions
              :result="turn.result"
              :request-id="
                getTurnMeta(index)?.requestId ??
                getTurnMeta(index)?.request?.requestId
              "
              @regenerate="handleRegenerate(index)"
            />
          </div>
        </div>
      </template>
    </div>

    <footer class="p-3 border-t border-border bg-card shrink-0">
      <div class="flex flex-col gap-2">
        <UiTextarea
          v-model="promptInput"
          :placeholder="
            props.labels?.inputPlaceholder ??
            t('ui.aiAssistant.inputPlaceholder')
          "
          :aria-label="
            props.labels?.inputLabel ?? t('ui.aiAssistant.inputLabel')
          "
          :rows="2"
          data-ai-assistant-input
          @keydown="handleKeydown"
        />
        <div class="flex items-center justify-end">
          <UiButton
            v-if="isGenerating"
            variant="secondary"
            size="sm"
            data-ai-assistant-stop
            @click="handleActiveStop"
          >
            {{ props.labels?.stop ?? t('ui.aiAssistant.stop') }}
          </UiButton>
          <UiButton
            v-else
            variant="primary"
            size="sm"
            :disabled="!promptInput.trim()"
            data-ai-assistant-send
            @click="handleSend"
          >
            {{ props.labels?.send ?? t('ui.aiAssistant.send') }}
          </UiButton>
        </div>
      </div>
    </footer>
  </section>
</template>
