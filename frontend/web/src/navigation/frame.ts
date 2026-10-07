import { inject, ref } from 'vue'
import type { InjectionKey, Ref } from 'vue'

export type SidebarMode = 'login' | 'menu' | 'collapsed'

export interface FrameContext {
  sidebar: Ref<SidebarMode>
  topbarHeight: Ref<number>
  sidebarElement: Ref<HTMLElement | null>
  mainElement: Ref<HTMLElement | null>
  layoutDependency: Ref<number>
  moved: () => void
}

export const frameContext: InjectionKey<FrameContext> = Symbol('frame')

export function createFrame(): FrameContext {
  const sidebar = ref<SidebarMode>('login')
  const topbarHeight = ref(54)
  const sidebarElement = ref<HTMLElement | null>(null)
  const mainElement = ref<HTMLElement | null>(null)
  const layoutDependency = ref(0)
  return {
    sidebar,
    topbarHeight,
    sidebarElement,
    mainElement,
    layoutDependency,
    moved: () => {
      layoutDependency.value++
    },
  }
}

export function useFrame(): FrameContext {
  const frame = inject(frameContext)
  if (!frame) throw new Error('AppFrame is required')
  return frame
}
