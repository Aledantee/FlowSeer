<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  aiActions,
  AiStaleError,
  isAiTargetElement,
  type AiResult,
  type AiRun,
  type AiSeed,
  type AiTarget,
  type AiTargetElement,
  type AiTargetSnapshot,
  type AiTargetView,
} from '../../ai'
import { useAiRegistry } from './context'
import UiContextMenu from '../context-menu/UiContextMenu.vue'
import UiContextMenuItem from '../context-menu/UiContextMenuItem.vue'
import UiPopover from '../popover/UiPopover.vue'
import UiAiLabel from './UiAiLabel.vue'
import UiAiResult, { type UiAiResultState } from './UiAiResult.vue'
import UiAiResultActions from './UiAiResultActions.vue'
import UiButton from '../button/UiButton.vue'

export interface UiAiContextLayerLabels {
  continueInAssistant?: string
}

export interface UiAiContextLayerProps {
  labels?: UiAiContextLayerLabels
}

const props = defineProps<UiAiContextLayerProps>()

const emit = defineEmits<{
  (e: 'continue', seed: AiSeed): void
}>()

const { t } = useI18n({ useScope: 'global' })
const registry = useAiRegistry()

const layerRoot = ref<HTMLElement>()
const activeTarget = ref<AiTarget>()
const activeVerb = ref<string>()
const verbs = ref<string[]>([])
const menuOpen = ref(false)

const popoverOpen = ref(false)
const popoverReference = ref<AiTargetElement>()
const currentRun = ref<AiRun>()
const currentResult = ref<AiResult>()
const resultState = ref<UiAiResultState>('idle')
const errorMessage = ref<string>()

const closeSymbol = '\u00D7'

let originElement: AiTargetElement | undefined
let preventMenuCloseAutoFocus = false
let currentToken = 0

function toSnapshot(t: AiTarget): AiTargetSnapshot {
  return {
    id: t.id,
    kind: t.kind,
    view: t.view ?? '',
    label: t.label,
    context: { ...t.context },
    ...(t.entity ? { entity: { ...t.entity } } : {}),
  }
}

function elementOf(node: Node | null): Element | null {
  return node instanceof Element ? node : (node?.parentElement ?? null)
}

// Walks every Element ancestor, so a context event that starts on an SVG child
// path still resolves the registered chart root.
function nearTarget(node: Node | null): AiTargetView | undefined {
  let element = elementOf(node)
  while (element && layerRoot.value?.contains(element)) {
    const id = isAiTargetElement(element)
      ? registry.idForElement(element)
      : undefined
    if (id) return registry.view(id)
    element = element.parentElement
  }
  return undefined
}

function handleContextMenuCapture(event: MouseEvent) {
  const target = event.target as Node | null
  const element = elementOf(target)

  // 1. the event originates in a[href], input, textarea, select, or [contenteditable]
  if (element) {
    const interactive = element.closest(
      'a[href], input, textarea, select, [contenteditable="true"], [contenteditable=""]',
    )
    if (interactive && layerRoot.value?.contains(interactive)) {
      event.stopPropagation()
      return
    }
  }

  // 2. the document has a non-empty text selection
  const selection = window.getSelection()
  if (selection && selection.toString().length > 0) {
    event.stopPropagation()
    return
  }

  // Resolve the nearest registered target
  const view = nearTarget(target)

  // 3. no target is found
  if (!view) {
    event.stopPropagation()
    return
  }

  // 4. the nearest target has kind 'view'
  if (view.target.kind === 'view') {
    event.stopPropagation()
    return
  }

  activeTarget.value = view.target
  originElement = isAiTargetElement(element) ? element : view.element
  verbs.value = aiActions(view.target)
}

function handleKeydown(event: KeyboardEvent) {
  const isShiftF10 =
    event.shiftKey && (event.key === 'F10' || event.code === 'F10')
  const isContextMenuKey =
    event.key === 'ContextMenu' || event.code === 'ContextMenu'
  if (!isShiftF10 && !isContextMenuKey) return

  const activeEl = document.activeElement
  if (!isAiTargetElement(activeEl) || !layerRoot.value?.contains(activeEl))
    return

  const targetView = nearTarget(activeEl)
  if (!targetView || targetView.target.kind === 'view') return

  event.preventDefault()
  event.stopPropagation()

  const rect = activeEl.getBoundingClientRect()
  const clientX = rect.left + rect.width / 2
  const clientY = rect.top + rect.height / 2

  activeEl.dispatchEvent(
    new MouseEvent('contextmenu', {
      bubbles: true,
      cancelable: true,
      clientX,
      clientY,
    }),
  )
}

function handleMenuCloseAutoFocus(event: Event) {
  if (preventMenuCloseAutoFocus) {
    event.preventDefault()
    preventMenuCloseAutoFocus = false
  }
}

function handlePopoverCloseAutoFocus(event: Event) {
  if (originElement && originElement.isConnected) {
    event.preventDefault()
    originElement.focus()
  }
}

function closePopover() {
  popoverOpen.value = false
  currentRun.value?.stop()
  currentRun.value = undefined
  currentResult.value = undefined
  resultState.value = 'idle'
  errorMessage.value = undefined
  popoverReference.value = undefined
}

function onPopoverOpenChange(open: boolean) {
  if (!open) {
    closePopover()
  }
}

async function startVerbRun(target: AiTarget, verb: string) {
  const token = ++currentToken
  const view = registry.view(target.id)
  if (!view) return

  preventMenuCloseAutoFocus = true
  activeVerb.value = verb
  popoverReference.value = view.element
  popoverOpen.value = true
  resultState.value = 'generating'
  currentResult.value = undefined
  errorMessage.value = undefined

  try {
    const run = registry.request(target, {
      action: verb,
      bound: true,
    })
    currentRun.value = run

    for await (const snapshot of run.snapshots) {
      if (token !== currentToken || !popoverOpen.value) return
      currentResult.value = snapshot
    }

    if (token === currentToken && popoverOpen.value) {
      resultState.value = 'done'
    }
  } catch (err: unknown) {
    if (token !== currentToken) return
    if (err instanceof AiStaleError) {
      closePopover()
      return
    }
    resultState.value = 'error'
    errorMessage.value =
      err instanceof Error ? err.message : t('ui.aiResult.errorGeneric')
  }
}

function handleSelectVerb(verb: string) {
  const target = activeTarget.value
  if (!target) return

  if (verb === 'Ask about this…') {
    emit('continue', {
      targets: [toSnapshot(target)],
      turns: [],
    })
    return
  }

  void startVerbRun(target, verb)
}

function handleStop() {
  currentRun.value?.stop()
  resultState.value = 'stopped'
}

function handleRegenerate() {
  const target = activeTarget.value
  const verb = activeVerb.value
  if (!target || !verb) return
  void startVerbRun(target, verb)
}

function handleContinue() {
  const target = activeTarget.value
  const verb = activeVerb.value
  const result = currentResult.value
  if (!target || !verb || !result) return

  const seed: AiSeed = {
    targets: [toSnapshot(target)],
    turns: [
      { role: 'user', prompt: verb },
      { role: 'assistant', result },
    ],
  }
  closePopover()
  emit('continue', seed)
}

let unsubscribe = () => {}

onMounted(() => {
  if (layerRoot.value) {
    layerRoot.value.addEventListener(
      'contextmenu',
      handleContextMenuCapture,
      true,
    )
    layerRoot.value.addEventListener('keydown', handleKeydown)
  }

  unsubscribe = registry.subscribe(() => {
    if (activeTarget.value && popoverOpen.value) {
      const live = registry.view(activeTarget.value.id)
      if (!live) {
        closePopover()
      }
    }
  })
})

onUnmounted(() => {
  unsubscribe()
  if (layerRoot.value) {
    layerRoot.value.removeEventListener(
      'contextmenu',
      handleContextMenuCapture,
      true,
    )
    layerRoot.value.removeEventListener('keydown', handleKeydown)
  }
  currentRun.value?.stop()
})
</script>

<template>
  <div ref="layerRoot" class="contents" data-ai-context-layer>
    <UiContextMenu
      v-model:open="menuOpen"
      @close-auto-focus="handleMenuCloseAutoFocus"
    >
      <template #trigger>
        <div class="contents">
          <slot />
        </div>
      </template>
      <UiContextMenuItem
        v-for="verb in verbs"
        :key="verb"
        :label="verb"
        @select="handleSelectVerb(verb)"
      />
    </UiContextMenu>

    <UiPopover
      :open="popoverOpen"
      :reference="popoverReference"
      side="bottom"
      align="start"
      :side-offset="6"
      @update:open="onPopoverOpenChange"
      @close-auto-focus="handlePopoverCloseAutoFocus"
    >
      <div
        class="flex flex-col gap-2 p-1 min-w-[20rem] max-w-sm"
        data-ai-context-popover
      >
        <div
          class="flex items-center justify-between pb-1 border-b border-border"
        >
          <UiAiLabel :run="currentRun" />
          <button
            type="button"
            class="text-muted-foreground hover:text-foreground text-xs px-1 rounded-sm cursor-pointer"
            :aria-label="t('ui.aiContextLayer.close')"
            @click="closePopover"
          >
            {{ closeSymbol }}
          </button>
        </div>

        <UiAiResult
          :run="currentRun"
          :result="currentResult"
          :state="resultState"
          :error="errorMessage"
          @stop="handleStop"
        />

        <div
          v-if="currentResult && resultState !== 'generating'"
          class="flex items-center justify-between gap-2 pt-2 border-t border-border"
        >
          <UiButton
            size="sm"
            variant="ghost"
            data-ai-continue
            @click="handleContinue"
          >
            {{
              props.labels?.continueInAssistant ??
              t('ui.aiContextLayer.continueInAssistant')
            }}
          </UiButton>
          <UiAiResultActions
            :result="currentResult"
            :request-id="currentRun?.requestId"
            @regenerate="handleRegenerate"
          />
        </div>
      </div>
    </UiPopover>
  </div>
</template>
