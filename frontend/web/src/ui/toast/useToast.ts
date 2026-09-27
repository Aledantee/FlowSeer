import { ref } from 'vue'

export type ToastVariant = 'default' | 'success' | 'warning' | 'danger'

export interface ToastAction {
  label: string
  altText?: string
  onClick: () => void
}

export interface ToastOptions {
  id?: string
  title: string
  description?: string
  variant?: ToastVariant
  duration?: number
  action?: ToastAction
}

export interface ToastItem extends ToastOptions {
  id: string
  open: boolean
}

const toasts = ref<ToastItem[]>([])

export function useToast() {
  function toast(options: ToastOptions): string {
    const id = options.id || Math.random().toString(36).substring(2, 9)
    const newToast: ToastItem = {
      ...options,
      id,
      open: true,
      variant: options.variant || 'default',
      duration: options.duration ?? 5000,
    }
    toasts.value.push(newToast)
    return id
  }

  function dismiss(id: string) {
    const target = toasts.value.find((t) => t.id === id)
    if (target) {
      target.open = false
    }
  }

  function remove(id: string) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  return {
    toasts,
    toast,
    dismiss,
    remove,
  }
}
