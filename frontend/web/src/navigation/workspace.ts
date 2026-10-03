import { inject } from 'vue'
import type { ComputedRef, InjectionKey, Ref } from 'vue'
import type { Device } from '../domain/fleet'
import type { PageContext, PageTarget } from './page'

export interface Move {
  deviceId: string
  name: string
  from: string
  to: string
  observed: boolean
  reverted: boolean
}

export type PaneId = 'main' | 'side'

// State every pane shares: one live fleet, one notice line, the actions
// that change the fleet, and how panes hand work to each other.
export interface Workspace {
  fleet: Ref<Device[]>
  // The message key for the notice line, or an empty string when clear.
  message: Ref<string>
  move: Ref<Move | undefined>
  undoMove: () => void
  dismissNotice: () => void
  reassign: (deviceId: string, siteId: string, reverted?: boolean) => void
  siteName: (id: string) => string
  tenantName: (siteId: string) => string
  // Follows a link from a page. With beside set it opens in the other pane,
  // with dock set it goes to the dock unopened; while clicks are linked, the
  // main pane's links to another page open in the side pane.
  follow: (
    page: PageContext,
    target: PageTarget,
    options?: { beside?: boolean; dock?: boolean },
  ) => Promise<void>
  activePane: Ref<PaneId>
  // Device ids the side pane steps through with the arrow keys, in the order
  // the list that opened the peek showed them.
  peek: Ref<string[]>
  // The device open in the side pane, which the main pane highlights.
  sideDeviceId: ComputedRef<string | undefined>
}

export const workspaceContext: InjectionKey<Workspace> = Symbol('workspace')

export function useWorkspace(): Workspace {
  const workspace = inject(workspaceContext)
  if (!workspace) throw new Error('Pages must render inside FleetView.')
  return workspace
}
