import type {
  AiHandler,
  AiRequest,
  AiRequestKind,
  AiTarget,
  AiTargetSegment,
  AiTargetView,
} from './types'

// One registry per document. The directive writes into it as elements mount
// and update; the window API and the on-screen action layer read from it.
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
  kind: AiRequestKind
  prompt?: string
}

export interface AiRegistry {
  register(element: HTMLElement, target: AiTarget): void
  unregister(element: HTMLElement): void
  list(): AiTarget[]
  view(id: string): AiTargetView | undefined
  idForElement(element: HTMLElement): string | undefined
  highlight(id: string): boolean
  clearHighlight(): void
  selection(): AiTargetView | undefined
  onRequest(handler: AiHandler): () => void
  hasHandler(): boolean
  request(target: AiTarget, options: AiRequestOptions): Promise<string>
  subscribe(listener: () => void): () => void
  // Re-evaluate the highlighted target after the viewport changed.
  refresh(): void
}

interface Registration {
  element: HTMLElement
  target: AiTarget
}

function defaultViewport(): AiViewport {
  const wide = window.matchMedia('(min-width: 561px)').matches
  return { wide, narrow: !wide }
}

function hiddenByAncestor(element: HTMLElement): boolean {
  for (let node: HTMLElement | null = element; node; node = node.parentElement)
    if (node.hidden) return true
  return false
}

// A view hands the directive a fresh target object on every render. When
// nothing but the object identity changed, re-registering it must not wake
// every listener, or a live value like traffic would churn the action layer.
function sameTarget(a: AiTarget, b: AiTarget): boolean {
  if (
    a.id !== b.id ||
    a.kind !== b.kind ||
    a.label !== b.label ||
    a.segment !== b.segment
  )
    return false
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
  const byElement = new Map<HTMLElement, string>()
  const listeners = new Set<() => void>()
  let handler: AiHandler | undefined
  let highlighted: string | undefined

  function notify() {
    for (const listener of [...listeners]) listener()
  }

  function isVisible(registration: Registration): boolean {
    if (!registration.element.isConnected) return false
    const segment: AiTargetSegment | undefined = registration.target.segment
    if (segment === 'mobile' && !viewportOf().narrow) return false
    if (segment === 'desktop' && !viewportOf().wide) return false
    return !hiddenByAncestor(registration.element)
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
  }

  function register(element: HTMLElement, target: AiTarget) {
    const held = byElement.get(element)
    if (held && held !== target.id) removeId(held)
    const existing = byId.get(target.id)
    if (existing && existing.element !== element) {
      console.warn(
        `[flowseerAi] Duplicate target id "${target.id}" ignored; the first registration stays.`,
      )
      return
    }
    if (existing && sameTarget(existing.target, target)) {
      existing.target = target
      return
    }
    byId.set(target.id, { element, target })
    byElement.set(element, target.id)
    notify()
  }

  function unregister(element: HTMLElement) {
    const held = byElement.get(element)
    if (!held) return
    removeId(held)
    notify()
  }

  function idForElement(element: HTMLElement): string | undefined {
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
    if (!view(id)) {
      if (highlighted !== undefined) {
        highlighted = undefined
        notify()
      }
      return false
    }
    if (highlighted !== id) {
      highlighted = id
      notify()
    }
    return true
  }

  function clearHighlight() {
    if (highlighted === undefined) return
    highlighted = undefined
    notify()
  }

  function selection(): AiTargetView | undefined {
    if (highlighted === undefined) return undefined
    const current = view(highlighted)
    if (!current) {
      highlighted = undefined
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

  function request(
    target: AiTarget,
    requestOptions: AiRequestOptions,
  ): Promise<string> {
    const registration = byId.get(target.id)
    if (!registration || !isVisible(registration))
      return Promise.reject(new AiStaleError())
    if (!handler) return Promise.reject(new AiUnavailableError())
    const snapshot: AiRequest = {
      requestId: nextRequestId(target.id),
      kind: requestOptions.kind,
      targetId: registration.target.id,
      label: registration.target.label,
      context: { ...registration.target.context },
      ...(requestOptions.prompt !== undefined
        ? { prompt: requestOptions.prompt }
        : {}),
    }
    const active = handler
    return Promise.resolve(active(snapshot)).then((answer) => {
      if (
        byId.get(target.id) !== registration ||
        !registration.element.isConnected
      )
        throw new AiStaleError()
      return answer
    })
  }

  function subscribe(listener: () => void): () => void {
    listeners.add(listener)
    return () => listeners.delete(listener)
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
    subscribe,
    refresh() {
      if (highlighted !== undefined) selection()
      else notify()
    },
  }
}
