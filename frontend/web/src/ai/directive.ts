import type { Directive } from 'vue'
import type { AiRegistry } from './registry'
import type { AiTarget } from './types'

// A view binds a target to the element that stands for it. Vue's `mounted`,
// `updated`, and `unmounted` hooks match that element's lifetime, so the
// registry follows rows as they are sorted, filtered, and swapped between
// panes without the view tracking DOM itself.
export function createAiTargetDirective(
  registry: AiRegistry,
): Directive<HTMLElement, AiTarget | undefined> {
  const cleanups = new WeakMap<HTMLElement, () => void>()

  function updateSelected(element: HTMLElement) {
    const isSelected = registry.selection()?.element === element
    if (isSelected) {
      element.setAttribute('data-ai-selected', '')
    } else {
      element.removeAttribute('data-ai-selected')
    }
  }

  return {
    mounted(element, binding) {
      if (binding.value) {
        registry.register(element, binding.value)
      }
      const unsubscribe = registry.subscribe(() => {
        updateSelected(element)
      })
      cleanups.set(element, unsubscribe)
      updateSelected(element)
    },
    updated(element, binding) {
      if (binding.value) {
        registry.register(element, binding.value)
      } else {
        registry.unregister(element)
      }
      updateSelected(element)
    },
    unmounted(element) {
      cleanups.get(element)?.()
      cleanups.delete(element)
      element.removeAttribute('data-ai-selected')
      registry.unregister(element)
    },
  }
}
