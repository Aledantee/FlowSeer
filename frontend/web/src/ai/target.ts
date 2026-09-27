import { inject } from 'vue'
import type { InjectionKey } from 'vue'
import type { AiTarget, AiTargetSegment } from './types'

// Every page mounts in a physical pane slot (`a` or `b`). The slot, not the
// pane's main/side role, is what stays stable across a swap, so it qualifies
// the target IDs a page registers. A page mounted outside the workspace, as
// in a Storybook story or a component test, uses `standalone`.
export type AiSlot = 'a' | 'b' | 'standalone'

export const aiSlot: InjectionKey<AiSlot> = Symbol('aiSlot')

export function useAiSlot(): AiSlot {
  return inject(aiSlot, 'standalone')
}

export interface AiTargetInput {
  slot: AiSlot
  view: string
  kind: string
  entityId: string
  label: string
  context: Record<string, string>
  segment?: AiTargetSegment
}

export function aiTargetId(input: {
  slot: AiSlot
  view: string
  kind: string
  entityId: string
  segment?: AiTargetSegment
}): string {
  const parts = [input.slot, input.view, input.kind]
  if (input.segment) parts.push(input.segment)
  parts.push(input.entityId)
  return parts.join(':')
}

export function aiTarget(input: AiTargetInput): AiTarget {
  const { slot, view, kind, entityId, label, context, segment } = input
  return {
    id: aiTargetId({ slot, view, kind, entityId, segment }),
    kind,
    label,
    context,
    ...(segment ? { segment } : {}),
  }
}
