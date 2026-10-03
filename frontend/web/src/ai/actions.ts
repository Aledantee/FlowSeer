import type { AiEntityRef } from './types'

export interface AiActionTarget {
  kind: string
  view?: string
  entity?: AiEntityRef
  context: Record<string, string>
}

// Supplies the context-menu verbs and the assistant panel's suggested prompts
// for a registered target or target snapshot.
export function aiActions(target: AiActionTarget): string[] {
  const verbs: string[] = []
  const entityKind =
    target.entity?.kind ??
    (target.kind === 'device' ||
    target.kind === 'attention-device' ||
    target.kind === 'role-device' ||
    target.kind === 'downlink'
      ? 'device'
      : target.kind === 'site'
        ? 'site'
        : target.kind === 'chart'
          ? 'chart'
          : target.kind === 'client'
            ? 'client'
            : undefined)

  const health = target.context.health

  if (entityKind === 'device') {
    if (health === 'Offline') {
      verbs.push('Why is this offline?')
    } else if (health === 'Degraded') {
      verbs.push('Why is this degraded?')
    }
    verbs.push('Summarize this device')
  } else if (entityKind === 'site') {
    verbs.push('Summarize this site')
  } else if (entityKind === 'chart') {
    verbs.push('Explain this traffic')
  } else if (entityKind === 'client') {
    verbs.push("Why is this client's signal weak?")
  } else if (target.kind === 'view') {
    if (target.view === 'dashboard') {
      verbs.push('Summarize this dashboard')
    } else if (target.view === 'device') {
      verbs.push('Summarize this device')
    }
  }

  verbs.push('Ask about this…')
  return verbs
}
