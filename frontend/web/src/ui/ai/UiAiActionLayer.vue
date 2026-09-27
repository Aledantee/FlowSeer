<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { AiStaleError, AiUnavailableError } from '../../ai'
import type { AiTargetView } from '../../ai'
import UiButton from '../button/UiButton.vue'
import UiPopover from '../popover/UiPopover.vue'
import UiTextarea from '../form/UiTextarea.vue'
import { useAiRegistry } from './context'
import { placeAsk, visibleRect } from './geometry'
import type { Rect } from './geometry'

// One overlay per app or Storybook canvas. It draws the selection outline and
// the compact Ask button over whichever target is selected, hovered, or
// focused, and hosts the prompt and answer. It is pointer-transparent except
// for the Ask button, so rows, charts, and panes keep their own controls and
// tab order.

const registry = useAiRegistry()

// The registry notifies on every registration change; mirroring it into a ref
// re-evaluates the active target and the geometry.
const version = ref(0)
let unsubscribe = () => {}

const selected = computed<AiTargetView | undefined>(() => {
  void version.value
  return registry.selection()
})
const hoveredId = ref<string>()
const focusedId = ref<string>()
let lastFocusedTargetId: string | undefined
let lastFocusedElement: HTMLElement | undefined
const hovered = computed(() =>
  hoveredId.value ? registry.view(hoveredId.value) : undefined,
)
const focused = computed(() =>
  focusedId.value ? registry.view(focusedId.value) : undefined,
)
const active = computed(() => focused.value ?? selected.value ?? hovered.value)

const open = ref(false)
const openForId = ref<string>()
let requestGeneration = 0
let returnFocusElement: HTMLElement | undefined
// While the panel is open it stays on the target it opened for, even if the
// pointer leaves that element.
const layerTarget = computed<AiTargetView | undefined>(() => {
  if (open.value && openForId.value) {
    const frozen = registry.view(openForId.value)
    if (frozen) return frozen
  }
  return active.value
})

const box = shallowRef<Rect>()
const layerRoot = ref<HTMLElement>()
const askButtonWidth = 26
const askButtonHeight = 22
const askButtonGap = 2
const viewportInset = 1

const outlineStyle = computed(() => {
  const rect = box.value
  if (!rect) return undefined
  return {
    top: `${rect.top}px`,
    left: `${rect.left}px`,
    width: `${rect.width}px`,
    height: `${rect.height}px`,
  }
})
// Ask is drawn only where placeAsk finds a spot that is both on screen and
// clear of every measured control. When none exists the trigger is hidden, but
// the popover stays mounted so an open panel is not torn down by the
// measurement.
const askPosition = computed(() => {
  const rect = box.value
  const element = layerTarget.value?.element
  if (!rect || !element) return undefined
  const controls = [
    ...document.querySelectorAll<HTMLElement>(
      'a[href], button, input, select, textarea, [role="button"], [role="link"]',
    ),
  ]
    .filter(
      (control) =>
        control !== element &&
        !layerRoot.value?.contains(control) &&
        !control.closest('[data-ai-ask-panel]'),
    )
    .map((control) => control.getBoundingClientRect())
    .filter((control) => control.width > 0 && control.height > 0)
  return placeAsk({
    target: rect,
    controls,
    viewport: {
      top: 0,
      left: 0,
      right: window.innerWidth,
      bottom: window.innerHeight,
      width: window.innerWidth,
      height: window.innerHeight,
    },
    size: { width: askButtonWidth, height: askButtonHeight },
    gap: askButtonGap,
    inset: viewportInset,
  })
})
const buttonStyle = computed(() => {
  const position = askPosition.value
  if (!position) return undefined
  return {
    top: `${position.top}px`,
    left: `${position.left}px`,
  }
})

function measure() {
  const element = layerTarget.value?.element
  box.value = element ? visibleRect(element) : undefined
}

let observer: ResizeObserver | undefined
watch(
  layerTarget,
  (view) => {
    measure()
    observer?.disconnect()
    if (view && typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(() => measure())
      observer.observe(view.element)
    }
  },
  { immediate: true, flush: 'post' },
)
watch(version, () => {
  if (open.value && openForId.value && !registry.view(openForId.value))
    closeAsk()
})

function nearTarget(node: Node | null): AiTargetView | undefined {
  let element =
    node instanceof HTMLElement ? node : (node?.parentElement ?? null)
  while (element) {
    const id = registry.idForElement(element)
    if (id) return registry.view(id)
    element = element.parentElement
  }
  return undefined
}

function handleFocusIn(event: FocusEvent) {
  const target = event.target
  const view = nearTarget(target as Node | null)
  if (!view && target instanceof Node && layerRoot.value?.contains(target))
    return
  focusedId.value = view?.target.id
  if (view && target instanceof HTMLElement) {
    lastFocusedTargetId = view.target.id
    lastFocusedElement = target
  }
}
function handleFocusOut(event: FocusEvent) {
  const next = event.relatedTarget as Node | null
  if (
    next &&
    (focused.value?.element.contains(next) || layerRoot.value?.contains(next))
  )
    return
  focusedId.value = undefined
}
function handlePointerOver(event: PointerEvent) {
  hoveredId.value = nearTarget(event.target as Node | null)?.target.id
}
function handlePointerOut(event: PointerEvent) {
  const next = event.relatedTarget as Node | null
  if (
    next &&
    (hovered.value?.element.contains(next) || layerRoot.value?.contains(next))
  )
    return
  hoveredId.value = undefined
}
function handleKeydown(event: KeyboardEvent) {
  if (!event.altKey || event.ctrlKey || event.metaKey || event.code !== 'KeyA')
    return
  const view = nearTarget(document.activeElement)
  if (!view) return
  event.preventDefault()
  registry.highlight(view.target.id)
  openAsk(view.target.id)
}

function handleResize() {
  registry.refresh()
  measure()
}

onMounted(() => {
  unsubscribe = registry.subscribe(() => {
    version.value += 1
  })
  window.addEventListener('resize', handleResize)
  window.addEventListener('keydown', handleKeydown)
  document.addEventListener('scroll', measure, true)
  document.addEventListener('focusin', handleFocusIn)
  document.addEventListener('focusout', handleFocusOut)
  document.addEventListener('pointerover', handlePointerOver)
  document.addEventListener('pointerout', handlePointerOut)
})
onUnmounted(() => {
  unsubscribe()
  observer?.disconnect()
  window.removeEventListener('resize', handleResize)
  window.removeEventListener('keydown', handleKeydown)
  document.removeEventListener('scroll', measure, true)
  document.removeEventListener('focusin', handleFocusIn)
  document.removeEventListener('focusout', handleFocusOut)
  document.removeEventListener('pointerover', handlePointerOver)
  document.removeEventListener('pointerout', handlePointerOut)
})

const prompt = ref('')
const pending = ref(false)
type AskState =
  | { kind: 'idle' }
  | { kind: 'answer'; answer: string }
  | { kind: 'error'; message: string }
  | { kind: 'unavailable' }
const state = ref<AskState>({ kind: 'idle' })

function openAsk(id: string) {
  requestGeneration += 1
  returnFocusElement =
    lastFocusedTargetId === id && lastFocusedElement?.isConnected
      ? lastFocusedElement
      : undefined
  openForId.value = id
  prompt.value = ''
  state.value = { kind: 'idle' }
  open.value = true
}
function closeAsk() {
  requestGeneration += 1
  open.value = false
  openForId.value = undefined
  prompt.value = ''
  pending.value = false
  state.value = { kind: 'idle' }
}
function onOpenChange(value: boolean) {
  if (value) open.value = true
  else closeAsk()
}
function handleCloseAutoFocus(event: Event) {
  const target = returnFocusElement
  returnFocusElement = undefined
  if (!target?.isConnected) return
  event.preventDefault()
  target.focus()
}

async function runAsk() {
  const view = layerTarget.value
  const text = prompt.value.trim()
  if (!view || !text || pending.value) return
  const generation = ++requestGeneration
  const targetId = view.target.id
  const isCurrent = () =>
    generation === requestGeneration &&
    open.value &&
    openForId.value === targetId
  pending.value = true
  state.value = { kind: 'idle' }
  try {
    const answer = await registry.request(view.target, {
      kind: 'ask',
      prompt: text,
    })
    if (!isCurrent()) return
    state.value = { kind: 'answer', answer }
  } catch (error: unknown) {
    if (!isCurrent()) return
    if (error instanceof AiStaleError) {
      closeAsk()
      return
    }
    if (error instanceof AiUnavailableError)
      state.value = { kind: 'unavailable' }
    else
      state.value = {
        kind: 'error',
        message:
          error instanceof Error ? error.message : 'Something went wrong.',
      }
  } finally {
    if (isCurrent()) pending.value = false
  }
}
function submit() {
  void runAsk()
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="box && layerTarget"
      ref="layerRoot"
      class="ai-layer"
      data-ai-action-layer=""
      :style="{ zIndex: 1 }"
    >
      <div class="ai-outline" :style="outlineStyle" aria-hidden="true"></div>
      <UiPopover
        :open="open"
        side="bottom"
        align="end"
        :side-offset="6"
        @update:open="onOpenChange"
        @close-auto-focus="handleCloseAutoFocus"
      >
        <template #trigger>
          <button
            v-show="askPosition"
            type="button"
            tabindex="-1"
            class="ai-ask"
            :style="buttonStyle"
            :aria-label="`Ask about ${layerTarget.target.label}`"
            @click="openAsk(layerTarget.target.id)"
          >
            Ask
          </button>
        </template>
        <form
          data-ai-ask-panel=""
          class="flex w-72 flex-col gap-2"
          @submit.prevent="submit"
        >
          <p class="text-xs font-medium text-foreground">
            Ask about
            <strong class="font-semibold">{{
              layerTarget.target.label
            }}</strong>
          </p>
          <UiTextarea
            v-model="prompt"
            :rows="3"
            aria-label="Your question"
            placeholder="Why is this device offline?"
          />
          <div class="flex items-center justify-end gap-2">
            <UiButton size="sm" variant="ghost" @click="closeAsk">
              Cancel
            </UiButton>
            <UiButton
              type="submit"
              size="sm"
              variant="primary"
              :disabled="!prompt.trim()"
              :loading="pending"
            >
              Ask
            </UiButton>
          </div>
          <p v-if="pending" role="status" class="sr-only">Asking…</p>
          <p
            v-else-if="state.kind === 'unavailable'"
            role="status"
            class="text-xs text-muted-foreground"
          >
            AI is unavailable
          </p>
          <p
            v-else-if="state.kind === 'error'"
            role="alert"
            class="text-xs text-danger-foreground"
          >
            {{ state.message }}
          </p>
          <p
            v-else-if="state.kind === 'answer'"
            role="status"
            class="text-xs whitespace-pre-wrap text-foreground"
          >
            {{ state.answer }}
          </p>
        </form>
      </UiPopover>
    </div>
  </Teleport>
</template>

<style scoped>
.ai-layer {
  position: fixed;
  inset: 0;
  pointer-events: none;
}
.ai-outline {
  position: fixed;
  pointer-events: none;
  border-radius: var(--radius-control);
  box-shadow: var(--ai-selection-ring);
}
.ai-ask {
  position: fixed;
  pointer-events: auto;
  width: 26px;
  height: 22px;
  padding: 0 2px;
  border-radius: var(--radius-control);
  border: 1px solid var(--border);
  background: var(--card);
  color: var(--foreground);
  font-size: var(--text-2xs);
  font-weight: 600;
  cursor: pointer;
}
.ai-ask:hover {
  background: var(--hover);
}
.ai-ask:focus-visible {
  outline: 2px solid var(--ring);
  outline-offset: 1px;
}
</style>
