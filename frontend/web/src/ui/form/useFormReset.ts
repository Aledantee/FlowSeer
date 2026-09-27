import { onMounted, onUnmounted, type Ref } from 'vue'

export interface UseFormResetOptions {
  elementRef: Ref<HTMLElement | { $el?: unknown } | null | undefined>
  onReset: () => void
}

function resolveElement(target: unknown): HTMLElement | null {
  if (target instanceof HTMLElement) {
    return target
  }
  if (
    typeof target === 'object' &&
    target !== null &&
    '$el' in target &&
    target.$el instanceof HTMLElement
  ) {
    return target.$el
  }
  return null
}

export function useFormReset(options: UseFormResetOptions): void {
  let form: HTMLFormElement | null = null

  function handleReset(event: Event): void {
    queueMicrotask(() => {
      if (event.defaultPrevented) return
      options.onReset()
    })
  }

  onMounted(() => {
    const el = resolveElement(options.elementRef.value)

    if (el) {
      form = el.closest('form')
      if (form) {
        form.addEventListener('reset', handleReset)
      }
    }
  })

  onUnmounted(() => {
    if (form) {
      form.removeEventListener('reset', handleReset)
      form = null
    }
  })
}
