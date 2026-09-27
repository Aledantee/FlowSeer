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
  return {
    mounted(element, binding) {
      if (binding.value) registry.register(element, binding.value)
    },
    updated(element, binding) {
      if (binding.value) registry.register(element, binding.value)
      else registry.unregister(element)
    },
    unmounted(element) {
      registry.unregister(element)
    },
  }
}
