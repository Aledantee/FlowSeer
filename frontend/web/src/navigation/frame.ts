import { inject, readonly, ref } from 'vue'
import type { InjectionKey, Ref } from 'vue'

export type SidebarMode = 'login' | 'menu' | 'collapsed'

export const FRAME_MOVE_SECONDS = 0.16
export const frameMove = {
  duration: FRAME_MOVE_SECONDS,
  ease: [0.2, 0, 0, 1] as [number, number, number, number],
}

export interface FrameContext {
  sidebar: Ref<SidebarMode>
  sidebarElement: Ref<HTMLElement | null>
  mainElement: Ref<HTMLElement | null>
  layoutDependency: Readonly<Ref<number>>
  moved: () => void
}

export const frameContext: InjectionKey<FrameContext> = Symbol('frame')

export function createFrame(): FrameContext {
  const sidebar = ref<SidebarMode>('login')
  const sidebarElement = ref<HTMLElement | null>(null)
  const mainElement = ref<HTMLElement | null>(null)
  const layoutDependency = ref(0)
  return {
    sidebar,
    sidebarElement,
    mainElement,
    layoutDependency: readonly(layoutDependency),
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
