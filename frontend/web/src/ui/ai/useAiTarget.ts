import {
  computed,
  getCurrentInstance,
  onScopeDispose,
  onUpdated,
  shallowRef,
  toValue,
  watch,
} from 'vue'
import type { ComputedRef, MaybeRefOrGetter } from 'vue'
import { cloneAiTarget, isAiTargetElement } from '../../ai'
import type { AiTarget, AiTargetElement } from '../../ai'
import { useAiRegistry } from './context'

// A template ref holds either the element or, for a child component, its
// instance. A component's `$el` is its rendered root, which is a text or
// comment node when the component renders nothing yet.
export function resolveAnchor(source: unknown): AiTargetElement | undefined {
  if (isAiTargetElement(source)) return source
  if (typeof source === 'object' && source !== null && '$el' in source) {
    return isAiTargetElement(source.$el) ? source.$el : undefined
  }
  return undefined
}

// The element a component's hooks act on. `$el` is not reactive: a child can
// swap its root without its ref changing, so the owner's own update re-reads
// it, as Reka's `useForwardExpose` does for the same reason.
export function useAnchorElement(
  source: MaybeRefOrGetter<unknown>,
): ComputedRef<AiTargetElement | undefined> {
  const updates = shallowRef(0)
  if (getCurrentInstance()) {
    onUpdated(() => {
      updates.value += 1
    })
  }
  return computed(() => {
    void updates.value
    return resolveAnchor(toValue(source))
  })
}

// Registers the anchor with the injected registry while a target is supplied.
// It reads the target's fields inside the watched getter, so an in-place edit
// of its metadata is seen as well as a new object. It unregisters only when
// the element or the identity changes, the target goes, or the owner
// unmounts, so an equal target update keeps a request that is bound to the
// registration.
export function useAiTarget(
  source: MaybeRefOrGetter<unknown>,
  target: () => AiTarget | undefined,
): void {
  const registry = useAiRegistry()
  const anchor = useAnchorElement(source)
  let held: { element: AiTargetElement; id: string } | undefined

  function release() {
    if (!held) return
    registry.unregister(held.element)
    held = undefined
  }

  watch(
    () => {
      const current = target()
      return {
        element: anchor.value,
        target: current ? cloneAiTarget(current) : undefined,
      }
    },
    ({ element, target: next }) => {
      if (held && (held.element !== element || held.id !== next?.id)) release()
      if (!element || !next) return
      registry.register(element, next)
      held = { element, id: next.id }
    },
    { flush: 'post', immediate: true },
  )

  onScopeDispose(release)
}
