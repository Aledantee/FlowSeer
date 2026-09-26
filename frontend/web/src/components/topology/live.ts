import { inject } from 'vue'
import type { ComputedRef, InjectionKey, Ref } from 'vue'
import type { Device, Link } from '../../domain/fleet'

export type Selection =
  | { kind: 'device' | 'link'; id: string }
  // A port is addressed by the device that owns it and its name.
  | { kind: 'port'; id: string; port: string }
// Graph nodes and edges hold only ids; they read live values through this
// so a traffic update re-renders them without re-running the layout.
export interface TopologyLive {
  fleet: ComputedRef<Device[]>
  // A device another pane has open, marked but not selected here.
  highlighted: ComputedRef<string | undefined>
  device: (id: string) => Device | undefined
  link: (id: string) => Link | undefined
  selection: Ref<Selection | undefined>
  // The link under the pointer; its port names show only while hovered.
  hovered: Ref<string | undefined>
  select: (selection: Selection | undefined) => void
}
export const topologyLive: InjectionKey<TopologyLive> = Symbol('topologyLive')

export function useTopologyLive(): TopologyLive {
  const live = inject(topologyLive)
  if (!live) throw new Error('Topology parts must render inside TopologyGraph.')
  return live
}
