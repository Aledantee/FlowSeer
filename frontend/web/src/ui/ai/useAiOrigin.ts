import { computed, shallowRef, watch, watchPostEffect } from 'vue'
import type { ComputedRef, MaybeRefOrGetter } from 'vue'
import type { AiOriginRequest } from './context'
import { useAnchorElement } from './useAiTarget'

// A user interaction with the value itself. Hover and programmatic focus are
// absent on purpose: neither one reads or changes the value.
const ACKNOWLEDGING_EVENTS = ['pointerdown', 'keydown', 'input', 'change']

// Elements whose caller supplied an origin, with the number of hooks on each.
// An interaction inside a nested origin belongs to that inner value, so the
// outer one ignores it and the two clear independently.
const anchors = new WeakMap<Element, number>()

export interface AiOrigin {
  // The request that is still marking the value, or undefined once the user
  // acknowledged it or the caller cleared it.
  active: ComputedRef<AiOriginRequest | undefined>
  // Acknowledges the active request. Portalled content of a compound control
  // calls it for interactions that never reach the anchor element.
  acknowledge: () => void
}

// Marks the anchor with `data-ai-origin="agent"` while the supplied request is
// unacknowledged. The mark is independent of selection and registration. The
// request ID is the identity: re-rendering an acknowledged request leaves it
// quiet, and a new ID marks the value again. The caller clears its own state
// on `onAcknowledged`, and until it does the hook remembers the request.
export function useAiOrigin(
  source: MaybeRefOrGetter<unknown>,
  origin: () => AiOriginRequest | undefined,
  onAcknowledged: (requestId: string) => void,
): AiOrigin {
  const anchor = useAnchorElement(source)
  const acknowledged = shallowRef<string>()
  const active = computed(() => {
    const current = origin()
    return current && current.requestId !== acknowledged.value
      ? current
      : undefined
  })

  watch(
    () => origin()?.requestId,
    (requestId) => {
      if (requestId === undefined) acknowledged.value = undefined
    },
    { flush: 'sync' },
  )

  function acknowledge() {
    const current = active.value
    if (!current) return
    acknowledged.value = current.requestId
    onAcknowledged(current.requestId)
  }

  function onInteraction(event: Event) {
    const element = anchor.value
    for (
      let node = event.target instanceof Node ? event.target : null;
      node && node !== element;
      node = node.parentNode
    ) {
      if (node instanceof Element && anchors.has(node)) return
    }
    acknowledge()
  }

  watchPostEffect((onCleanup) => {
    const element = anchor.value
    if (!element || origin() === undefined) return
    anchors.set(element, (anchors.get(element) ?? 0) + 1)
    onCleanup(() => {
      const count = (anchors.get(element) ?? 1) - 1
      if (count > 0) anchors.set(element, count)
      else anchors.delete(element)
    })
    if (!active.value) return
    element.setAttribute('data-ai-origin', 'agent')
    for (const type of ACKNOWLEDGING_EVENTS) {
      element.addEventListener(type, onInteraction, true)
    }
    onCleanup(() => {
      element.removeAttribute('data-ai-origin')
      for (const type of ACKNOWLEDGING_EVENTS) {
        element.removeEventListener(type, onInteraction, true)
      }
    })
  })

  return { active, acknowledge }
}
