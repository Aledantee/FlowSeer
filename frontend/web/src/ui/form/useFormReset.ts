import { onMounted, onUnmounted, type Ref } from 'vue'

export interface UseFormResetOptions {
  elementRef: Ref<HTMLElement | { $el?: unknown } | null | undefined>
  onReset: () => void
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
    const raw = options.elementRef.value
    const el =
      raw instanceof HTMLElement
        ? raw
        : raw &&
            typeof raw === 'object' &&
            '$el' in raw &&
            (raw as { $el: unknown }).$el instanceof HTMLElement
          ? (raw as { $el: HTMLElement }).$el
          : null

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
