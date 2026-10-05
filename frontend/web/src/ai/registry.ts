import { validateAiResult } from './validate'
import type {
  AiAnswer,
  AiHandler,
  AiRequest,
  AiResult,
  AiRun,
  AiTarget,
  AiTargetElement,
  AiTargetSegment,
  AiTargetSnapshot,
  AiTargetView,
  AiTurn,
} from './types'

// One registry per document. Components write into it as elements mount and
// update; the window API and the on-screen action layer read from it. The
// registry also owns `data-ai-selected` on the element it highlights, so every
// registered element, however it was registered, is marked the same way.
// Duplicate IDs are invalid: the first registration of an ID wins, so a
// second element cannot silently shadow the instance an agent selected.

export interface AiViewport {
  wide: boolean
  narrow: boolean
}

export interface AiRegistryOptions {
  viewport?: () => AiViewport
}

// No handler is installed: Ask or summary reports the unavailable state
// rather than inventing an answer.
export class AiUnavailableError extends Error {
  constructor() {
    super('AI is unavailable')
    this.name = 'AiUnavailableError'
  }
}

// The registration behind a request is gone or replaced before the answer
// came back, so the answer refers to an instance that is no longer on screen.
export class AiStaleError extends Error {
  constructor() {
    super('The target is no longer mounted')
    this.name = 'AiStaleError'
  }
}

export interface AiRequestOptions {
  action: string
  prompt?: string
  history?: AiTurn[]
  bound?: boolean
}

export interface FeedbackPayload {
  requestId: string
  rating: 'up' | 'down'
}

export interface AiRegistry {
  register(element: AiTargetElement, target: AiTarget): void
  unregister(element: AiTargetElement): void
  list(): AiTarget[]
  view(id: string): AiTargetView | undefined
  idForElement(element: AiTargetElement): string | undefined
  highlight(id: string): boolean
  clearHighlight(): void
  selection(): AiTargetView | undefined
  onRequest(handler: AiHandler): () => void
  hasHandler(): boolean
  request(
    targets: AiTarget | AiTarget[] | AiTargetSnapshot | AiTargetSnapshot[],
    options: AiRequestOptions,
  ): AiRun
  feedback(requestId: string, rating: 'up' | 'down'): void
  onFeedback(listener: (payload: FeedbackPayload) => void): () => void
  subscribe(listener: () => void): () => void
  // Re-evaluate the highlighted target after the viewport changed.
  refresh(): void
}

interface Registration {
  element: AiTargetElement
  target: AiTarget
}

// Components resolve their anchor from a ref or a forwarded component root,
// so the value is checked before it reaches the registry.
export function isAiTargetElement(node: unknown): node is AiTargetElement {
  return node instanceof HTMLElement || node instanceof SVGElement
}

// A caller may edit its target object in place. The registry keeps its own
// copy so the next registration is compared with what it last held.
export function cloneAiTarget(target: AiTarget): AiTarget {
  return {
    id: target.id,
    kind: target.kind,
    ...(target.view !== undefined ? { view: target.view } : {}),
    label: target.label,
    context: { ...target.context },
    ...(target.entity ? { entity: { ...target.entity } } : {}),
    ...(target.segment ? { segment: target.segment } : {}),
  }
}

function prepareSnapshot(value: unknown): AiResult {
  const snapshot =
    typeof value === 'string'
      ? { type: 'answer', text: value, refs: [] }
      : value
  const validated = validateAiResult(snapshot)
  if (validated.type !== 'answer') return validated

  // The handler's `ui` is read once, and the answer is rebuilt so that no
  // accessor of the handler's object can answer for `ui` later.
  let ui: unknown
  try {
    const handlerUi: unknown = validated.ui
    ui = handlerUi === undefined ? undefined : structuredClone(handlerUi)
  } catch {
    ui = null
  }
  const answer: AiAnswer = {
    type: 'answer',
    text: validated.text,
    refs: validated.refs,
  }
  const summary = validated.summary
  if (summary !== undefined) answer.summary = summary
  if (ui !== undefined) answer.ui = ui
  return answer
}

function defaultViewport(): AiViewport {
  const wide = window.matchMedia('(min-width: 561px)').matches
  return { wide, narrow: !wide }
}

function isHidden(element: AiTargetElement): boolean {
  for (let node: Element | null = element; node; node = node.parentElement) {
    if ('hidden' in node && node.hidden === true) return true
    const win = node.ownerDocument?.defaultView ?? window
    const style = win.getComputedStyle(node)
    if (
      style.display === 'none' ||
      style.visibility === 'hidden' ||
      style.visibility === 'collapse'
    )
      return true
  }
  return false
}

// A view hands a component a fresh target object on every render. When
// nothing but the object identity changed, re-registering it must not wake
// every listener, or a live value like traffic would churn the action layer.
function sameTarget(a: AiTarget, b: AiTarget): boolean {
  if (
    a.id !== b.id ||
    a.kind !== b.kind ||
    a.view !== b.view ||
    a.label !== b.label ||
    a.segment !== b.segment
  )
    return false
  if (
    a.entity?.kind !== b.entity?.kind ||
    a.entity?.id !== b.entity?.id ||
    a.entity?.label !== b.entity?.label
  ) {
    return false
  }
  const aKeys = Object.keys(a.context)
  const bKeys = Object.keys(b.context)
  return (
    aKeys.length === bKeys.length &&
    aKeys.every((key) => a.context[key] === b.context[key])
  )
}

let idCounter = 0
function nextRequestId(targetId: string): string {
  idCounter += 1
  return `${targetId}#${idCounter}`
}

export function createAiRegistry(options: AiRegistryOptions = {}): AiRegistry {
  const viewportOf = options.viewport ?? defaultViewport
  const byId = new Map<string, Registration>()
  const byElement = new Map<AiTargetElement, string>()
  const listeners = new Set<() => void>()
  const feedbackListeners = new Set<(payload: FeedbackPayload) => void>()
  let handler: AiHandler | undefined
  let highlighted: string | undefined
  let marked: AiTargetElement | undefined

  // The attribute follows the selection and nothing else: it sits on the
  // element of the highlighted registration and is removed from any other.
  function syncSelection() {
    const next =
      highlighted === undefined ? undefined : byId.get(highlighted)?.element
    if (marked === next) return
    marked?.removeAttribute('data-ai-selected')
    next?.setAttribute('data-ai-selected', '')
    marked = next
  }

  function notify() {
    for (const listener of [...listeners]) listener()
  }

  function isVisible(registration: Registration): boolean {
    if (!registration.element.isConnected) return false
    const segment: AiTargetSegment | undefined = registration.target.segment
    if (segment === 'mobile' && !viewportOf().narrow) return false
    if (segment === 'desktop' && !viewportOf().wide) return false
    return !isHidden(registration.element)
  }

  function view(id: string): AiTargetView | undefined {
    const registration = byId.get(id)
    if (!registration || !isVisible(registration)) return undefined
    return { target: registration.target, element: registration.element }
  }

  function removeId(id: string) {
    const registration = byId.get(id)
    if (!registration) return
    byId.delete(id)
    byElement.delete(registration.element)
    if (highlighted === id) highlighted = undefined
    syncSelection()
  }

  function register(element: AiTargetElement, target: AiTarget) {
    const held = byElement.get(element)
    if (held && held !== target.id) removeId(held)
    const existing = byId.get(target.id)
    if (existing && existing.element !== element) {
      console.warn(
        `[flowseerAi] Duplicate target id "${target.id}" ignored; the first registration stays.`,
      )
      return
    }
    if (existing && existing.element === element) {
      if (sameTarget(existing.target, target)) return
      existing.target = cloneAiTarget(target)
      notify()
      return
    }
    byId.set(target.id, { element, target: cloneAiTarget(target) })
    byElement.set(element, target.id)
    notify()
  }

  function unregister(element: AiTargetElement) {
    const held = byElement.get(element)
    if (!held) return
    removeId(held)
    notify()
  }

  function idForElement(element: AiTargetElement): string | undefined {
    const id = byElement.get(element)
    if (id === undefined) return undefined
    const registration = byId.get(id)
    return registration && isVisible(registration) ? id : undefined
  }

  function list(): AiTarget[] {
    return [...byId.values()]
      .filter(isVisible)
      .map((registration) => registration.target)
      .sort((a, b) => a.id.localeCompare(b.id))
  }

  function highlight(id: string): boolean {
    const targetView = view(id)
    if (!targetView) {
      if (highlighted !== undefined) {
        highlighted = undefined
        syncSelection()
        notify()
      }
      return false
    }
    if (highlighted !== id) {
      highlighted = id
      syncSelection()
      notify()
    }
    targetView.element.scrollIntoView({ block: 'nearest', inline: 'nearest' })
    return true
  }

  function clearHighlight() {
    if (highlighted === undefined) return
    highlighted = undefined
    syncSelection()
    notify()
  }

  function selection(): AiTargetView | undefined {
    if (highlighted === undefined) return undefined
    const current = view(highlighted)
    if (!current) {
      highlighted = undefined
      syncSelection()
      notify()
      return undefined
    }
    return current
  }

  function onRequest(next: AiHandler): () => void {
    handler = next
    notify()
    return () => {
      if (handler === next) {
        handler = undefined
        notify()
      }
    }
  }

  function feedback(requestId: string, rating: 'up' | 'down') {
    const payload: FeedbackPayload = { requestId, rating }
    for (const listener of [...feedbackListeners]) {
      listener(payload)
    }
  }

  function onFeedback(
    listener: (payload: FeedbackPayload) => void,
  ): () => void {
    feedbackListeners.add(listener)
    return () => {
      feedbackListeners.delete(listener)
    }
  }

  function subscribe(listener: () => void): () => void {
    listeners.add(listener)
    return () => listeners.delete(listener)
  }

  function request(
    targets: AiTarget | AiTarget[] | AiTargetSnapshot | AiTargetSnapshot[],
    requestOptions: AiRequestOptions,
  ): AiRun {
    const targetList = Array.isArray(targets) ? targets : [targets]
    const isBound = requestOptions.bound ?? false
    const boundElements: AiTargetElement[] = []

    if (isBound) {
      for (const t of targetList) {
        const reg = byId.get(t.id)
        if (!reg || !isVisible(reg)) {
          throw new AiStaleError()
        }
        boundElements.push(reg.element)
      }
    }

    if (!handler) {
      throw new AiUnavailableError()
    }

    const primaryId = targetList[0]?.id ?? 'request'
    const requestId = nextRequestId(primaryId)
    const activeHandler = handler

    const targetSnapshots: AiTargetSnapshot[] = targetList.map((t) => ({
      id: t.id,
      kind: t.kind,
      view: t.view ?? '',
      label: t.label,
      context: { ...t.context },
      ...(t.entity ? { entity: { ...t.entity } } : {}),
    }))

    const frozenTargets = Object.freeze(
      targetSnapshots.map((t) =>
        Object.freeze({
          ...t,
          context: Object.freeze({ ...t.context }),
          ...(t.entity ? { entity: Object.freeze({ ...t.entity }) } : {}),
        }),
      ),
    )

    const frozenRequest: Omit<AiRequest, 'signal'> = Object.freeze({
      requestId,
      action: requestOptions.action,
      kind:
        requestOptions.action === 'ask'
          ? 'ask'
          : requestOptions.action === 'summary'
            ? 'summary'
            : undefined,
      targetId: targetSnapshots[0]?.id,
      context: targetSnapshots[0]?.context,
      targets: frozenTargets,
      history: Object.freeze([...(requestOptions.history ?? [])]),
      ...(requestOptions.prompt !== undefined
        ? { prompt: requestOptions.prompt }
        : {}),
    }) as unknown as Omit<AiRequest, 'signal'>

    const controller = new AbortController()
    const fullRequest: AiRequest = {
      ...frozenRequest,
      signal: controller.signal,
    }

    const result = activeHandler(fullRequest)

    function isStale(): boolean {
      if (!isBound) return false
      for (let i = 0; i < targetList.length; i++) {
        const t = targetList[i]!
        const boundEl = boundElements[i]!
        const reg = byId.get(t.id)
        if (!reg || reg.element !== boundEl || !isVisible(reg)) {
          return true
        }
      }
      return false
    }

    async function* generateSnapshots(): AsyncGenerator<AiResult> {
      if (isStale()) {
        throw new AiStaleError()
      }

      let rejectStale: ((err: AiStaleError) => void) | undefined
      const stalePromise = new Promise<never>((_, reject) => {
        rejectStale = reject
      })
      const unsubscribeStale = isBound
        ? subscribe(() => {
            if (isStale()) {
              rejectStale?.(new AiStaleError())
            }
          })
        : undefined

      try {
        if (result != null && Symbol.asyncIterator in (result as object)) {
          const iterator = (result as AsyncIterable<AiResult>)[
            Symbol.asyncIterator
          ]()
          try {
            while (true) {
              if (controller.signal.aborted) {
                return
              }
              if (isStale()) {
                throw new AiStaleError()
              }
              const nextPromise = iterator.next()
              const { value, done } = await (isBound
                ? Promise.race([nextPromise, stalePromise])
                : nextPromise)
              if (done) {
                return
              }
              if (isStale()) {
                throw new AiStaleError()
              }
              if (controller.signal.aborted) {
                return
              }
              yield prepareSnapshot(value)
            }
          } finally {
            if (typeof iterator.return === 'function') {
              try {
                await iterator.return()
              } catch {
                // Ignore return error
              }
            }
          }
        } else {
          const valuePromise = Promise.resolve(
            result as Promise<AiResult | string>,
          )
          const value = await (isBound
            ? Promise.race([valuePromise, stalePromise])
            : valuePromise)
          if (isStale()) {
            throw new AiStaleError()
          }
          if (controller.signal.aborted) {
            return
          }
          yield prepareSnapshot(value)
        }
      } finally {
        unsubscribeStale?.()
      }
    }

    return {
      requestId,
      request: frozenRequest,
      snapshots: generateSnapshots(),
      stop: () => {
        controller.abort()
      },
    }
  }

  return {
    register,
    unregister,
    list,
    view,
    idForElement,
    highlight,
    clearHighlight,
    selection,
    onRequest,
    hasHandler: () => handler !== undefined,
    request,
    feedback,
    onFeedback,
    subscribe,
    refresh() {
      if (highlighted !== undefined) selection()
      else notify()
    },
  }
}
